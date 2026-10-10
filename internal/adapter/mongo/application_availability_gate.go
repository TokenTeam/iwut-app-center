package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
)

type applicationAvailabilityGate int

const (
	applicationGateMissing applicationAvailabilityGate = iota
	applicationGateBlocked
	applicationGateAvailable
)

// readApplicationAvailabilityGate distinguishes a normal lifecycle/platform/archive
// gate from corrupt state. Callers map the three outcomes to the
// privacy semantics of their own capability.
func readApplicationAvailabilityGate(ctx context.Context, database *m.Database, applicationID string) (applicationAvailabilityGate, error) {
	var document applicationDocument
	err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": applicationID}).Decode(&document)
	if errors.Is(err, m.ErrNoDocuments) {
		return applicationGateMissing, nil
	}
	if err != nil {
		return applicationGateMissing, err
	}
	availability, err := availabilityFromDocument(document)
	if err != nil {
		return applicationGateMissing, err
	}
	if err := (&ApplicationOperationsRepository{database: database}).validateLatestEvent(ctx, document, availability); err != nil {
		return applicationGateMissing, err
	}
	archive, err := archiveStateFromDocument(document)
	if err != nil {
		return applicationGateMissing, err
	}
	if err := (&ApplicationArchivalRepository{database: database}).validateLatestArchiveEvent(ctx, document, archive); err != nil {
		return applicationGateMissing, err
	}
	if availability.LifecycleStatus != "ACTIVE" || availability.Status != "AVAILABLE" || archive.ArchiveStatus != "UNARCHIVED" {
		return applicationGateBlocked, nil
	}
	return applicationGateAvailable, nil
}
