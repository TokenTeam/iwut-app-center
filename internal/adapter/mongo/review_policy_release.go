package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

// A release is a fixed input of a reviewed deployment migration. It is not a
// runtime CRUD command and is never accepted from HTTP/gRPC callers.
type reviewPolicyRelease struct {
	MigrationID, Kind                   string
	From, To                            versionReviewPolicyDocument
	ApprovalReference, ReleaseReference string
}

type reviewPolicyReleaseReceipt struct {
	ID           string    `bson:"_id"`
	Kind         string    `bson:"policyKind"`
	FromVersion  string    `bson:"fromVersion"`
	ToVersion    string    `bson:"toVersion"`
	ManifestHash string    `bson:"manifestHash"`
	AppliedAt    time.Time `bson:"appliedAt"`
}

func immutablePolicyEqual(a, b versionReviewPolicyDocument) bool {
	a.Status, b.Status = "ACTIVE", "ACTIVE"
	return equalVersionReviewPolicyDocuments(a, b)
}

// This narrow helper lets a dedicated migration atomically record its success
// with the policy switch. Reentering a historical migration never reactivates
// its target, including after a later release has retired it.
func (m *Migrator) applyReviewPolicyRelease(ctx context.Context, release reviewPolicyRelease, at time.Time) (*reviewPolicyReleaseReceipt, error) {
	if m == nil || m.database == nil || at.IsZero() || strings.TrimSpace(release.MigrationID) != release.MigrationID || release.MigrationID == "" || release.ApprovalReference == "" || release.ReleaseReference == "" || release.From.Version == release.To.Version || release.From.Status != "ACTIVE" || release.To.Status != "ACTIVE" {
		return nil, errors.New("invalid fixed policy release")
	}
	collection := versionReviewPoliciesCollectionName
	switch release.Kind {
	case "VERSION":
	case "PROFILE":
		collection = profileReviewPoliciesCollectionName
	default:
		return nil, errors.New("invalid policy kind")
	}
	for _, d := range []versionReviewPolicyDocument{release.From, release.To} {
		if err := validateReleasePolicy(release.Kind, d); err != nil {
			return nil, err
		}
	}
	// Coordination counters are transaction inputs, not immutable manifest data.
	manifest := release
	manifest.From.CoordinationRevision, manifest.To.CoordinationRevision = 0, 0
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	receipt := reviewPolicyReleaseReceipt{ID: release.MigrationID, Kind: release.Kind, FromVersion: release.From.Version, ToVersion: release.To.Version, ManifestHash: hex.EncodeToString(hash[:]), AppliedAt: at.UTC().Truncate(time.Millisecond)}
	session, err := m.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		policies := m.database.Collection(collection)
		ledger := m.database.Collection(migrationLedgerCollectionName)
		var existing reviewPolicyReleaseReceipt
		err := ledger.FindOne(tx, bson.M{"_id": receipt.ID}).Decode(&existing)
		if err == nil {
			if existing.Kind != receipt.Kind || existing.FromVersion != receipt.FromVersion || existing.ToVersion != receipt.ToVersion || existing.ManifestHash != receipt.ManifestHash || existing.AppliedAt.IsZero() {
				return nil, errors.New("policy release identity or manifest differs")
			}
			for _, want := range []versionReviewPolicyDocument{release.From, release.To} {
				var got versionReviewPolicyDocument
				if err := policies.FindOne(tx, bson.M{"version": want.Version}).Decode(&got); err != nil {
					return nil, err
				}
				if validateReleasePolicy(release.Kind, got) != nil || !immutablePolicyEqual(got, want) {
					return nil, errors.New("policy release history is inconsistent")
				}
			}
			cursor, err := policies.Find(tx, bson.M{"status": "ACTIVE"}, options.Find().SetLimit(2))
			if err != nil {
				return nil, err
			}
			defer cursor.Close(tx)
			var active []versionReviewPolicyDocument
			if err := cursor.All(tx, &active); err != nil {
				return nil, err
			}
			if len(active) != 1 || validateReleasePolicy(release.Kind, active[0]) != nil {
				return nil, errors.New("policy release current active fact is inconsistent")
			}
			return &existing, nil
		}
		if !errors.Is(err, dm.ErrNoDocuments) {
			return nil, err
		}
		active, err := policies.Find(tx, bson.M{"status": "ACTIVE"}, options.Find().SetLimit(2))
		if err != nil {
			return nil, err
		}
		defer active.Close(tx)
		var docs []versionReviewPolicyDocument
		if err := active.All(tx, &docs); err != nil {
			return nil, err
		}
		if len(docs) != 1 || !immutablePolicyEqual(docs[0], release.From) || validateReleasePolicy(release.Kind, docs[0]) != nil || docs[0].CoordinationRevision < 1 || docs[0].CoordinationRevision == math.MaxInt64 {
			return nil, errors.New("policy release baseline differs")
		}
		if err := policies.FindOne(tx, bson.M{"version": release.To.Version}).Err(); !errors.Is(err, dm.ErrNoDocuments) {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("policy release target already exists")
		}
		updated, err := policies.UpdateOne(tx, bson.M{"version": release.From.Version, "status": "ACTIVE", "coordinationRevision": docs[0].CoordinationRevision}, bson.M{"$set": bson.M{"status": "RETIRED"}, "$inc": bson.M{"coordinationRevision": int64(1)}})
		if err != nil {
			return nil, err
		}
		if updated.MatchedCount != 1 {
			return nil, errors.New("policy release baseline changed")
		}
		next := release.To
		next.CoordinationRevision = 1
		if _, err := policies.InsertOne(tx, next); err != nil {
			return nil, err
		}
		if _, err := ledger.InsertOne(tx, receipt); err != nil {
			return nil, err
		}
		return &receipt, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("apply fixed policy release: %w", err)
	}
	return result.(*reviewPolicyReleaseReceipt), nil
}

func validateReleasePolicy(kind string, d versionReviewPolicyDocument) error {
	if kind == "VERSION" {
		_, err := versionReviewPolicyFromDocument(d)
		return err
	}
	encoded, err := bson.Marshal(d)
	if err != nil {
		return err
	}
	_, _, err = profilePolicyFromRaw(encoded)
	return err
}
