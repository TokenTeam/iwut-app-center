package mongo

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBRPUB035StrictPublicationRejectsDamagedFields(t *testing.T) {
	id := "0199b33c-d041-7abc-8abc-123456789012"
	at := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	base := bson.D{{Key: "publicationId", Value: id}, {Key: "applicationId", Value: id}, {Key: "rpcApiMajor", Value: int32(1)}, {Key: "testVersionId", Value: id}, {Key: "stableVersionId", Value: id}, {Key: "greyRollout", Value: bson.D{{Key: "rolloutId", Value: id}, {Key: "versionId", Value: id}, {Key: "exposureBasisPoints", Value: int32(500)}, {Key: "cohortSeed", Value: make([]byte, 32)}}}, {Key: "revision", Value: int64(2)}, {Key: "createdBy", Value: "admin"}, {Key: "createdAt", Value: at}, {Key: "updatedBy", Value: "admin"}, {Key: "updatedAt", Value: at}}
	for _, tc := range []struct {
		name   string
		mutate func(bson.D) bson.D
	}{
		{"valid", func(d bson.D) bson.D { return d }},
		{"missing field", func(d bson.D) bson.D { return d[1:] }},
		{"unknown field", func(d bson.D) bson.D { return append(d, bson.E{Key: "extra", Value: "x"}) }},
		{"duplicate field", func(d bson.D) bson.D { return append(d, d[0]) }},
		{"invalid ID", func(d bson.D) bson.D { d[3].Value = "broken"; return d }},
		{"int32 revision", func(d bson.D) bson.D { d[6].Value = int32(2); return d }},
		{"Grey without Stable", func(d bson.D) bson.D { d[4].Value = nil; return d }},
		{"Grey missing field", func(d bson.D) bson.D { d[5].Value = d[5].Value.(bson.D)[1:]; return d }},
		{"Grey extra field", func(d bson.D) bson.D {
			d[5].Value = append(d[5].Value.(bson.D), bson.E{Key: "extra", Value: "x"})
			return d
		}},
		{"Grey duplicate field", func(d bson.D) bson.D { g := d[5].Value.(bson.D); d[5].Value = append(g[:3:3], g[0]); return d }},
		{"Grey seed length", func(d bson.D) bson.D {
			g := append(bson.D{}, d[5].Value.(bson.D)...)
			g[3].Value = make([]byte, 31)
			d[5].Value = g
			return d
		}},
		{"Grey exposure range", func(d bson.D) bson.D {
			g := append(bson.D{}, d[5].Value.(bson.D)...)
			g[2].Value = int32(10001)
			d[5].Value = g
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := bson.Marshal(tc.mutate(append(bson.D{}, base...)))
			if err != nil {
				t.Fatal(err)
			}
			p, err := restoreStrictPublication(raw)
			if tc.name == "valid" {
				if err != nil || p == nil {
					t.Fatal(err)
				}
			} else if err == nil || p != nil {
				t.Fatal("accepted corruption", p)
			}
		})
	}
}

func TestTestPublicationClearMigrationIntegration_UCAPP036_StrictHistoryCombinations(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	seed := createApprovedPublicationVersion(t, db, "clear-validator")
	repo := NewApplicationPublicationRepository(db)
	placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed)
	result, err := repo.ClearTest(t.Context(), seed.applicationID, 1, seed.adminID, 1, clearTestAudit(t))
	if err != nil {
		t.Fatal(err)
	}
	history := applicationPublicationHistoryToDocument(result.History())
	for _, tc := range []struct {
		name  string
		key   string
		value any
		omit  bool
	}{
		{"Review", "approvedReviewId", seed.reviewID.String(), false},
		{"new target", "newVersionId", seed.versionID.String(), false},
		{"missing previous", "previousVersionId", nil, false},
		{"scope", "scopeCatalogRevision", int64(1), false},
		{"preflight", "preflightPolicyVersion", "v1", false},
		{"Grey ID", "greyRolloutId", seed.reviewID.String(), false},
		{"previous exposure", "previousExposureBasisPoints", int32(500), false},
		{"new exposure", "newExposureBasisPoints", int32(500), false},
		{"seed", "cohortSeed", make([]byte, 32), false},
		{"first revision", "publicationRevision", int64(1), false},
		{"missing null field", "approvedReviewId", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bytes, _ := bson.Marshal(history)
			var d bson.M
			_ = bson.Unmarshal(bytes, &d)
			d["historyId"] = nextIntegrationApplicationReviewID(t).String()
			d["publicationId"] = nextIntegrationApplicationReviewID(t).String()
			if tc.omit {
				delete(d, tc.key)
			} else {
				d[tc.key] = tc.value
			}
			if _, err := db.Collection(applicationPublicationHistoryCollectionName).InsertOne(t.Context(), d); err == nil {
				t.Fatal("validator accepted", tc.name)
			}
		})
	}
	if err := NewMigrator(db).Migrate(t.Context()); err != nil {
		t.Fatal("idempotency", err)
	}
}
