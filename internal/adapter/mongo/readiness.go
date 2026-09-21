package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
)

// RequiredMigrationID is the newest migration the running service requires
// before it will serve traffic. It mirrors the last entry of Migrator.Migrate:
// the composed service ships the UC-APP-005 decision schema, so an older
// revision is not sufficient.
const RequiredMigrationID = versionReviewPolicyMigrationID

// MigrationLedgerCollectionName is the read-only ledger written by Migrator.
// Startup only reads it; it never creates the collection or an index.
const MigrationLedgerCollectionName = migrationLedgerCollectionName

const mongoShardedTopologyMessage = "isdbgrid"

// topologyObservation is the subset of the `hello` command response that
// decides whether multi-document transactions are available.
type topologyObservation struct {
	SetName string `bson:"setName"`
	Msg     string `bson:"msg"`
}

// supportsTransactions is a pure predicate over an observed hello response. A
// replica set exposes setName; a mongos exposes msg=isdbgrid. A standalone
// server exposes neither.
func supportsTransactions(observation topologyObservation) bool {
	return observation.SetName != "" || observation.Msg == mongoShardedTopologyMessage
}

// requiredMigrationRecorded is a pure predicate over the applied migration IDs.
func requiredMigrationRecorded(applied []string) bool {
	for _, id := range applied {
		if id == RequiredMigrationID {
			return true
		}
	}
	return false
}

// VerifyTransactionTopology performs the read-only topology check shared by
// the migration command and normal service startup. Running migrations against
// a standalone server would create a schema the service can never use, so both
// entry points reject it before doing any work.
func VerifyTransactionTopology(ctx context.Context, client *drivermongo.Client) error {
	if client == nil {
		return errors.New("verify mongo topology: client is nil")
	}
	var observation topologyObservation
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&observation); err != nil {
		return fmt.Errorf("verify mongo topology: %w", err)
	}
	if !supportsTransactions(observation) {
		return fmt.Errorf("verify mongo topology: standalone MongoDB does not support the transactions this service requires; deploy a replica set or mongos")
	}
	return nil
}

// VerifyDeploymentReadiness performs read-only startup checks before the
// service serves traffic: the connected MongoDB must support multi-document
// transactions and must already record RequiredMigrationID. It never creates a
// collection, index or validator; explicit migration remains a deployment step.
func VerifyDeploymentReadiness(ctx context.Context, database *drivermongo.Database) error {
	if database == nil {
		return errors.New("verify mongo deployment: database is nil")
	}
	if err := VerifyTransactionTopology(ctx, database.Client()); err != nil {
		return err
	}

	applied, err := appliedMigrationIDs(ctx, database)
	if err != nil {
		return err
	}
	if !requiredMigrationRecorded(applied) {
		return fmt.Errorf("verify mongo schema: required migration %q is not recorded (applied: %d); run the migrate command before serving", RequiredMigrationID, len(applied))
	}
	return nil
}

func appliedMigrationIDs(ctx context.Context, database *drivermongo.Database) ([]string, error) {
	cursor, err := database.Collection(MigrationLedgerCollectionName).Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer func() {
		_ = cursor.Close(ctx)
	}()

	var records []migrationRecord
	if err := cursor.All(ctx, &records); err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids, nil
}
