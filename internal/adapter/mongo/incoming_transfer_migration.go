package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const incomingTransferMigrationID = "0041_incoming_application_admin_transfer_query"
const incomingTransferIndexName = "ix_application_admin_transfer_target_pending_requested"

func (m *Migrator) applyIncomingTransferMigration(ctx context.Context) error {
	_, err := m.database.Collection(applicationAdminTransfersCollectionName).Indexes().CreateOne(ctx, dm.IndexModel{Keys: bson.D{{Key: "toAdminId", Value: 1}, {Key: "status", Value: 1}, {Key: "requestedAt", Value: -1}, {Key: "transferId", Value: -1}}, Options: options.Index().SetName(incomingTransferIndexName)})
	return err
}
