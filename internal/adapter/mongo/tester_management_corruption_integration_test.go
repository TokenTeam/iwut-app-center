package mongo

import (
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	td "iwut-app-center/internal/tester/domain"
	tp "iwut-app-center/internal/tester/port"
	"testing"
)

func TestUCAPP037_BR_TST_042_DuplicateBSONFieldsIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, field := range []string{"ownershipRevision", "membershipId", "joinedAt", "status", "removedBy", "joinLinkId", "createdAt", "createdBy"} {
		t.Run(field, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "management-bson-duplicates")
			member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "tester")).Membership()
			collection := applicationTesterMembershipsCollectionName
			filter := bson.M{"membershipId": member.MembershipID().String()}
			if field == "ownershipRevision" {
				collection = applicationsCollectionName
				filter = bson.M{"id": link.ApplicationID().String()}
			} else if field == "joinLinkId" || field == "createdAt" || field == "createdBy" {
				collection = applicationTesterJoinLinksCollectionName
				filter = bson.M{"joinLinkId": link.JoinLinkID().String()}
			}
			var d bson.D
			if err := db.Collection(collection).FindOne(t.Context(), filter).Decode(&d); err != nil {
				t.Fatal(err)
			}
			for _, e := range d {
				if e.Key == field {
					d = append(d, e)
					break
				}
			}
			if _, err := db.Collection(collection).ReplaceOne(t.Context(), filter, d, options.Replace().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
			repo := testerManagementRepository(t, db)
			var err error
			if field == "ownershipRevision" {
				_, err = repo.GetManagement(t.Context(), link.CreatedBy(), link.ApplicationID())
			} else if collection == applicationTesterJoinLinksCollectionName {
				_, err = repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
			} else {
				_, err = repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
			}
			if !errors.Is(err, tp.ErrManagementStateInconsistent) {
				t.Fatal("duplicate BSON key accepted", err)
			}
		})
	}
}
func TestUCAPP037_BR_TST_042_ListReadUniquenessAndCapacityIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, kind := range []string{"membership-id", "active-user", "link-id", "active-link", "101-active"} {
		t.Run(kind, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "management-page-duplicates")
			m := newIntegrationTesterMembership(t, link, "tester")
			joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, m)
			repo := testerManagementRepository(t, db)
			switch kind {
			case "membership-id":
				if err := db.Collection(applicationTesterMembershipsCollectionName).Indexes().DropOne(t.Context(), testerMembershipIDUniqueIndexName); err != nil {
					t.Fatal(err)
				}
				d := testerMembershipToDocument(m)
				d.TesterAuthID = "other"
				if _, err := db.Collection(applicationTesterMembershipsCollectionName).InsertOne(t.Context(), d); err != nil {
					t.Fatal(err)
				}
			case "active-user":
				if err := db.Collection(applicationTesterMembershipsCollectionName).Indexes().DropOne(t.Context(), testerMembershipActiveUniqueIndexName); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Collection(applicationTesterMembershipsCollectionName).InsertOne(t.Context(), testerMembershipToDocument(newIntegrationTesterMembership(t, link, "tester"))); err != nil {
					t.Fatal(err)
				}
			case "link-id":
				if err := db.Collection(applicationTesterJoinLinksCollectionName).Indexes().DropOne(t.Context(), testerJoinLinkIDUniqueIndexName); err != nil {
					t.Fatal(err)
				}
				d := testerJoinLinkToDocument(newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy()))
				d.JoinLinkID = link.JoinLinkID().String()
				d.Status = "REVOKED"
				reason, by := "MANUAL", link.CreatedBy().String()
				at := link.CreatedAt()
				d.RevocationReason = &reason
				d.RevokedBy = &by
				d.RevokedAt = &at
				if _, err := db.Collection(applicationTesterJoinLinksCollectionName).InsertOne(t.Context(), d); err != nil {
					t.Fatal(err)
				}
			case "active-link":
				if err := db.Collection(applicationTesterJoinLinksCollectionName).Indexes().DropOne(t.Context(), testerJoinLinkActiveUniqueIndexName); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Collection(applicationTesterJoinLinksCollectionName).InsertOne(t.Context(), testerJoinLinkToDocument(newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy()))); err != nil {
					t.Fatal(err)
				}
			case "101-active":
				seedTesterMemberships(t, db, link, 100)
			}
			var err error
			if kind == "link-id" || kind == "active-link" {
				_, err = repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), td.JoinLinkQueryAll, 1, "")
			} else {
				size := int32(1)
				if kind == "101-active" {
					size = 100
				}
				_, err = repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), td.MembershipQueryAll, size, "")
			}
			if !errors.Is(err, tp.ErrManagementStateInconsistent) {
				t.Fatal("read page invariant accepted", err)
			}
		})
	}
}
