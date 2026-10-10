package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
)

var errActiveVersionAbandonmentOverlap = errors.New("active application version overlaps immutable abandonment")

// ensureNoActiveVersionAbandonmentOverlap makes management queries fail closed
// if an immutable abandonment ever coexists with the active identity or
// sequence it retired.
func ensureNoActiveVersionAbandonmentOverlap(ctx context.Context, repository *drivermongo.Database, applicationIDs []string) error {
	if len(applicationIDs) == 0 {
		return nil
	}
	cursor, err := repository.Collection(applicationVersionAbandonmentsCollectionName).Find(ctx, bson.M{"applicationId": bson.M{"$in": applicationIDs}})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	var facts []applicationVersionAbandonmentDocument
	if err := cursor.All(ctx, &facts); err != nil {
		return err
	}
	if len(facts) == 0 {
		return nil
	}
	conditions := make(bson.A, 0, len(facts)*2)
	for _, fact := range facts {
		conditions = append(conditions,
			bson.M{"versionId": fact.VersionID},
			bson.M{"applicationId": fact.ApplicationID, "sequence": fact.Sequence},
		)
	}
	count, err := repository.Collection(applicationVersionsCollectionName).CountDocuments(ctx, bson.M{"$or": conditions})
	if err != nil {
		return err
	}
	if count != 0 {
		return errActiveVersionAbandonmentOverlap
	}
	return nil
}
