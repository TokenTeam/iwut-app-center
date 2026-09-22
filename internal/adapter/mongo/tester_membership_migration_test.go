package mongo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	testerdomain "iwut-app-center/internal/tester/domain"
)

func TestApplicationTesterMembershipMigrationIntegration(t *testing.T) {
	client := integrationClient(t)
	db, link := testerMembershipFixture(t, client, "tester-membership-schema")
	collection := db.Collection(applicationTesterMembershipsCollectionName)
	base := testerMembershipToDocument(newIntegrationTesterMembership(t, link, "tester"))
	if _, err := collection.InsertOne(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	t.Run("BR-TST-016 named identity and ACTIVE partial unique indexes", func(t *testing.T) {
		for _, test := range []struct {
			name, index string
			mutate      func(*applicationTesterMembershipDocument)
		}{
			{"membership ID", testerMembershipIDUniqueIndexName, func(d *applicationTesterMembershipDocument) { d.TesterAuthID = "different-tester" }},
			{"ACTIVE application and tester", testerMembershipActiveUniqueIndexName, func(d *applicationTesterMembershipDocument) {
				d.MembershipID = nextIntegrationTesterJoinLinkID(t).String()
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				d := base
				test.mutate(&d)
				_, err := collection.InsertOne(t.Context(), d)
				if !drivermongo.IsDuplicateKeyError(err) || !strings.Contains(err.Error(), test.index) {
					t.Fatalf("expected index %s", test.index)
				}
			})
		}
		// A different application may contain the same tester, while any number of
		// removed episodes can coexist with the one active pair.
		different := base
		different.MembershipID = nextIntegrationTesterJoinLinkID(t).String()
		different.ApplicationID = nextIntegrationTesterJoinLinkID(t).String()
		if _, err := collection.InsertOne(t.Context(), different); err != nil {
			t.Fatal(err)
		}
		actor := "admin"
		at := time.Now().UTC()
		for range 2 {
			removed := base
			removed.MembershipID = nextIntegrationTesterJoinLinkID(t).String()
			removed.Status = "REMOVED"
			removed.RemovedBy = &actor
			removed.RemovedAt = &at
			if _, err := collection.InsertOne(t.Context(), removed); err != nil {
				t.Fatal(err)
			}
		}
		cursor, err := collection.Indexes().List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close(t.Context())
		var indexes []bson.M
		if err = cursor.All(t.Context(), &indexes); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{testerMembershipIDUniqueIndexName, testerMembershipActiveUniqueIndexName, testerMembershipApplicationAuditIndexName, testerMembershipUserAuditIndexName} {
			found := false
			for _, index := range indexes {
				if index["name"] == name {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing index %s", name)
			}
		}
	})
	t.Run("BR-TST-015 BR-TST-016 BR-TST-019 validator enforces audit shape and rejects sensitive fields", func(t *testing.T) {
		raw, err := bson.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name   string
			mutate func(bson.M)
		}{
			{"missing membership ID", func(d bson.M) { delete(d, "membershipId") }},
			{"invalid membership ID", func(d bson.M) { d["membershipId"] = "invalid" }},
			{"invalid application ID", func(d bson.M) { d["applicationId"] = "invalid" }},
			{"invalid source link", func(d bson.M) { d["joinedViaJoinLinkId"] = "invalid" }},
			{"empty auth ID", func(d bson.M) { d["testerAuthId"] = "" }},
			{"missing joined time", func(d bson.M) { delete(d, "joinedAt") }},
			{"invalid joined time", func(d bson.M) { d["joinedAt"] = "2026-01-01" }},
			{"missing removedBy", func(d bson.M) { delete(d, "removedBy") }},
			{"missing removedAt", func(d bson.M) { delete(d, "removedAt") }},
			{"active removed actor", func(d bson.M) { d["removedBy"] = "admin" }},
			{"active removed time", func(d bson.M) { d["removedAt"] = time.Now() }},
			{"removed missing audit", func(d bson.M) { d["status"] = "REMOVED" }},
			{"removed missing actor", func(d bson.M) { d["status"] = "REMOVED"; d["removedAt"] = time.Now() }},
			{"removed missing time", func(d bson.M) { d["status"] = "REMOVED"; d["removedBy"] = "admin" }},
			{"unknown status", func(d bson.M) { d["status"] = "INVITED" }},
			{"secret", func(d bson.M) { d["secret"] = "sensitive" }},
			{"hash", func(d bson.M) { d["tokenHash"] = make([]byte, 32) }},
			{"URL", func(d bson.M) { d["joinUrl"] = "https://example/#secret=sensitive" }},
			{"publication", func(d bson.M) { d["versionId"] = nextIntegrationTesterJoinLinkID(t).String() }},
		} {
			t.Run(test.name, func(t *testing.T) {
				var d bson.M
				if err := bson.Unmarshal(raw, &d); err != nil {
					t.Fatal(err)
				}
				d["membershipId"] = nextIntegrationTesterJoinLinkID(t).String()
				d["testerAuthId"] = "validator-user"
				test.mutate(d)
				_, err := collection.InsertOne(t.Context(), d)
				var serverError drivermongo.ServerError
				if !errors.As(err, &serverError) || !serverError.HasErrorCode(121) {
					t.Fatalf("expected document validation failure for %s", test.name)
				}
			})
		}
	})
	t.Run("constructor does not create schema and migration is explicit idempotent", func(t *testing.T) {
		empty := integrationDatabase(t, client)
		_ = NewApplicationTesterMembershipRepository(empty)
		names, err := empty.ListCollectionNames(t.Context(), bson.D{})
		if err != nil || len(names) != 0 {
			t.Fatalf("constructor names=%v err=%v", names, err)
		}
		if err = NewMigrator(db).Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		n, err := db.Collection(migrationLedgerCollectionName).CountDocuments(t.Context(), bson.D{{Key: "_id", Value: applicationTesterMembershipMigrationID}})
		if err != nil || n != 1 {
			t.Fatalf("migration records=%d error=%v", n, err)
		}
	})
}

func TestTesterMembershipDocumentMapping(t *testing.T) {
	link := newIntegrationTesterJoinLink(t, "01890f49-0000-7000-8000-000000000001", "admin")
	m := newIntegrationTesterMembership(t, link, "tester")
	d := testerMembershipToDocument(m)
	actual, err := testerMembershipFromDocument(d)
	if err != nil || !reflect.DeepEqual(actual, m) {
		t.Fatalf("roundtrip error=%v", err)
	}
	actor := "admin"
	at := time.Now().UTC()
	d.Status = "REMOVED"
	d.RemovedBy = &actor
	d.RemovedAt = &at
	actual, err = testerMembershipFromDocument(d)
	if err != nil || actual.Status() != testerdomain.MembershipStatusRemoved || !reflect.DeepEqual(testerMembershipToDocument(actual), d) {
		t.Fatalf("removed roundtrip error=%v", err)
	}
	d.RemovedBy = nil
	if _, err = testerMembershipFromDocument(d); err == nil {
		t.Fatal("invalid removed episode decoded")
	}
}

func TestTesterMembershipRepositoryRejectsInvalidInputWithoutDatabaseCalls(t *testing.T) {
	link := newIntegrationTesterJoinLink(t, "01890f49-0000-7000-8000-000000000001", "admin")
	m := newIntegrationTesterMembership(t, link, "tester")
	var nilRepo *ApplicationTesterMembershipRepository
	for _, repo := range []*ApplicationTesterMembershipRepository{nilRepo, NewApplicationTesterMembershipRepository(nil)} {
		if result, err := repo.ResolveJoinCandidate(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes()); err == nil || result != nil {
			t.Fatal("nil database resolve succeeded")
		}
		if result, err := repo.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), m.TesterAuthID(), m, 100); err == nil || result != nil {
			t.Fatal("nil database join succeeded")
		}
	}
}

func TestTesterMembershipPersistenceErrorsAreSafe_BR_TST_019(t *testing.T) {
	sensitive := "secret-hash-or-other-user"
	for _, input := range []error{errors.New(sensitive), drivermongo.WriteException{WriteErrors: drivermongo.WriteErrors{{Code: 11000, Message: sensitive}}}, drivermongo.CommandError{Code: 121, Message: sensitive}, context.Canceled, context.DeadlineExceeded} {
		err := safeTesterMembershipError(input)
		for _, render := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(render, sensitive) {
				t.Fatal("sensitive driver cause exposed")
			}
		}
		if errors.Unwrap(err) != nil {
			t.Fatal("driver cause retained")
		}
	}
}
