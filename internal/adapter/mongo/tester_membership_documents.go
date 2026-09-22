package mongo

import (
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	"time"
)

type applicationTesterMembershipDocument struct {
	MembershipID        string     `bson:"membershipId"`
	ApplicationID       string     `bson:"applicationId"`
	TesterAuthID        string     `bson:"testerAuthId"`
	Status              string     `bson:"status"`
	JoinedViaJoinLinkID string     `bson:"joinedViaJoinLinkId"`
	JoinedAt            time.Time  `bson:"joinedAt"`
	RemovedBy           *string    `bson:"removedBy"`
	RemovedAt           *time.Time `bson:"removedAt"`
}

func testerMembershipToDocument(m *testerdomain.ApplicationTesterMembership) applicationTesterMembershipDocument {
	d := applicationTesterMembershipDocument{MembershipID: m.MembershipID().String(), ApplicationID: m.ApplicationID().String(), TesterAuthID: m.TesterAuthID().String(), Status: string(m.Status()), JoinedViaJoinLinkID: m.JoinedViaJoinLinkID().String(), JoinedAt: m.JoinedAt(), RemovedAt: m.RemovedAt()}
	if actor := m.RemovedBy(); actor != nil {
		v := actor.String()
		d.RemovedBy = &v
	}
	return d
}
func testerMembershipFromDocument(d applicationTesterMembershipDocument) (*testerdomain.ApplicationTesterMembership, error) {
	var removedBy *shared.AuthID
	if d.RemovedBy != nil {
		v := shared.AuthID(*d.RemovedBy)
		removedBy = &v
	}
	return testerdomain.RestoreTesterMembership(testerdomain.ApplicationTesterMembershipID(d.MembershipID), shared.ApplicationID(d.ApplicationID), shared.AuthID(d.TesterAuthID), testerdomain.MembershipStatus(d.Status), testerdomain.ApplicationTesterJoinLinkID(d.JoinedViaJoinLinkID), d.JoinedAt, removedBy, d.RemovedAt)
}
