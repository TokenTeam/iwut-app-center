package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
	td "iwut-app-center/internal/tester/domain"
	tp "iwut-app-center/internal/tester/port"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUCAPP037_BR_TST_037_AllRetainedStateCombinationsIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, lifecycle := range []string{"ACTIVE", "CLOSING", "CLOSED"} {
		for _, suspended := range []bool{false, true} {
			for _, archived := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-suspended%t-archived%t", lifecycle, suspended, archived), func(t *testing.T) {
					db, link := testerMembershipFixture(t, client, "management-states")
					repo := testerManagementRepository(t, db)
					joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "tester"))
					now := time.Now().UTC().Truncate(time.Millisecond)
					if suspended {
						if _, err := NewApplicationOperationsRepository(db).Set(t.Context(), link.ApplicationID(), "platform-operator", ad.PlatformAvailabilitySuspended, 1, 1, "management state", ad.ApplicationOperationEventID(nextIntegrationTesterJoinLinkID(t)), now); err != nil {
							t.Fatal(err)
						}
					}
					if archived {
						if _, err := NewApplicationArchivalRepository(db).SetArchiveState(t.Context(), link.ApplicationID(), link.CreatedBy(), ad.ApplicationArchiveArchived, 1, func() (ad.ApplicationArchiveEventID, time.Time, error) {
							return ad.ApplicationArchiveEventID(nextIntegrationTesterJoinLinkID(t)), now, nil
						}); err != nil {
							t.Fatal(err)
						}
					}
					if lifecycle != "ACTIVE" {
						id := ad.ApplicationClosureID(nextIntegrationTesterJoinLinkID(t))
						proof := ad.ApplicationCloseProof{Subject: link.CreatedBy(), ApplicationID: link.ApplicationID(), JTI: nextIntegrationTesterJoinLinkID(t).String(), AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
						closure := NewApplicationClosureRepository(db)
						if _, err := closure.Start(t.Context(), link.ApplicationID(), link.CreatedBy(), 1, 1, proof, id, now); err != nil {
							t.Fatal(err)
						}
						if lifecycle == "CLOSED" {
							if _, err := closure.Complete(t.Context(), id, "test-receipt", now.Add(time.Second)); err != nil {
								t.Fatal(err)
							}
						}
					}
					before := readTesterLinkApplication(t, db, link.ApplicationID())
					overview, err := repo.GetManagement(t.Context(), link.CreatedBy(), link.ApplicationID())
					if err != nil || overview.ActiveTesterCount != 1 {
						t.Fatal("retained overview", err)
					}
					page, err := repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
					if err != nil || len(page.Items) != 1 {
						t.Fatal("retained membership", err)
					}
					links, err := repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
					if err != nil || len(links.Items) != 1 {
						t.Fatal("retained link", err)
					}
					if lifecycle != "ACTIVE" && (links.Items[0].RevocationReason == nil || *links.Items[0].RevocationReason != td.RevocationReasonApplicationClosure) {
						t.Fatal("closure reason projection")
					}
					if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
						t.Fatal("query changed retained state")
					}
					if _, err = repo.GetManagement(t.Context(), "tester", link.ApplicationID()); !errors.Is(err, tp.ErrManagementNotFound) {
						t.Fatal("tester implicit access")
					}
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					if _, err = repo.GetManagement(ctx, link.CreatedBy(), link.ApplicationID()); !errors.Is(err, tp.ErrManagementUnavailable) {
						t.Fatal("driver cancellation category", err)
					}
				})
			}
		}
	}
}
func TestUCAPP037_BR_TST_040_041_042_LinkTiesCanonicalAndIndexesIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("same stored time mixed states UUID binary order and page size continuation", func(t *testing.T) {
		db := reusableIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "admin", "link-order")
		at := time.Now().UTC().Truncate(time.Millisecond)
		ids := []td.ApplicationTesterJoinLinkID{"01890f47-0000-7000-8000-00000000370a", "01890f47-0000-7000-8000-00000000370b", "01890f47-0000-7000-8000-00000000370c"}
		var expected *td.ApplicationTesterJoinLinkID
		for _, id := range ids {
			random := newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())
			link, err := td.NewActiveTesterJoinLink(id, app.ID(), random.TokenHash(), app.AdminID(), at)
			if err != nil {
				t.Fatal(err)
			}
			createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), app.ID(), app.AdminID(), expected, link)
			copy := id
			expected = &copy
		}
		repo := testerManagementRepository(t, db)
		p, err := repo.ListJoinLinks(t.Context(), app.AdminID(), app.ID(), "ALL", 1, "")
		if err != nil || p.Items[0].JoinLinkID != ids[2] || p.NextPageToken == "" {
			t.Fatal("first link page", err)
		}
		next, err := repo.ListJoinLinks(t.Context(), app.AdminID(), app.ID(), "ALL", 2, p.NextPageToken)
		if err != nil || len(next.Items) != 2 || next.Items[0].JoinLinkID != ids[1] || next.Items[1].JoinLinkID != ids[0] || next.NextPageToken != "" {
			t.Fatal("link ties", err)
		}
		for _, tc := range []struct {
			collection string
			models     []string
			want       []bson.D
		}{{applicationTesterMembershipsCollectionName, []string{testerMembershipTimeIndex, testerMembershipStatusTimeIndex}, []bson.D{{{Key: "applicationId", Value: int32(1)}, {Key: "joinedAt", Value: int32(-1)}, {Key: "membershipId", Value: int32(-1)}}, {{Key: "applicationId", Value: int32(1)}, {Key: "status", Value: int32(1)}, {Key: "joinedAt", Value: int32(-1)}, {Key: "membershipId", Value: int32(-1)}}}}, {applicationTesterJoinLinksCollectionName, []string{testerLinkTimeIndex, testerLinkStatusTimeIndex}, []bson.D{{{Key: "applicationId", Value: int32(1)}, {Key: "createdAt", Value: int32(-1)}, {Key: "joinLinkId", Value: int32(-1)}}, {{Key: "applicationId", Value: int32(1)}, {Key: "status", Value: int32(1)}, {Key: "createdAt", Value: int32(-1)}, {Key: "joinLinkId", Value: int32(-1)}}}}} {
			cursor, err := db.Collection(tc.collection).Indexes().List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var docs []struct {
				Name string `bson:"name"`
				Key  bson.D `bson:"key"`
			}
			if err = cursor.All(t.Context(), &docs); err != nil {
				t.Fatal(err)
			}
			for i, name := range tc.models {
				found := false
				for _, d := range docs {
					if d.Name == name {
						found = true
						if !reflect.DeepEqual(d.Key, tc.want[i]) {
							t.Fatal("index key order", d.Key)
						}
					}
				}
				if !found {
					t.Fatal("index missing", name)
				}
			}
		}
	})
	for _, field := range []string{"membershipId", "joinedViaJoinLinkId", "linkId", "replacementId"} {
		t.Run("noncanonical-"+field, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "management-canonical")
			member := newIntegrationTesterMembership(t, link, "tester")
			joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, member)
			collection := applicationTesterMembershipsCollectionName
			filter := bson.M{"membershipId": member.MembershipID().String()}
			fields := bson.M{}
			switch field {
			case "membershipId":
				fields[field] = strings.ToUpper(member.MembershipID().String())
			case "joinedViaJoinLinkId":
				fields[field] = strings.ToUpper(link.JoinLinkID().String())
			case "linkId":
				collection = applicationTesterJoinLinksCollectionName
				filter = bson.M{"joinLinkId": link.JoinLinkID().String()}
				fields["joinLinkId"] = strings.ToUpper(link.JoinLinkID().String())
			case "replacementId":
				collection = applicationTesterJoinLinksCollectionName
				filter = bson.M{"joinLinkId": link.JoinLinkID().String()}
				id := link.JoinLinkID()
				replacement := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), link.ApplicationID(), link.CreatedBy(), &id, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())).JoinLink()
				fields["replacedByJoinLinkId"] = strings.ToUpper(replacement.JoinLinkID().String())
			}
			if _, err := db.Collection(collection).UpdateOne(t.Context(), filter, bson.M{"$set": fields}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
			repo := testerManagementRepository(t, db)
			var err error
			if field == "linkId" || field == "replacementId" {
				_, err = repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
			} else {
				_, err = repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
			}
			if !errors.Is(err, tp.ErrManagementStateInconsistent) {
				t.Fatal("noncanonical persisted UUID accepted", err)
			}
		})
	}
}
