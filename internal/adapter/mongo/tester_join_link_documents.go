package mongo

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
)

type applicationTesterJoinLinkDocument struct {
	JoinLinkID           string      `bson:"joinLinkId"`
	ApplicationID        string      `bson:"applicationId"`
	TokenHash            bson.Binary `bson:"tokenHash"`
	Status               string      `bson:"status"`
	CreatedBy            string      `bson:"createdBy"`
	CreatedAt            time.Time   `bson:"createdAt"`
	RevokedBy            *string     `bson:"revokedBy"`
	RevokedAt            *time.Time  `bson:"revokedAt"`
	RevocationReason     *string     `bson:"revocationReason"`
	ReplacedByJoinLinkID *string     `bson:"replacedByJoinLinkId"`
}

func testerJoinLinkToDocument(link *testerdomain.ApplicationTesterJoinLink) applicationTesterJoinLinkDocument {
	hash := link.TokenHash().Bytes()
	d := applicationTesterJoinLinkDocument{
		JoinLinkID: link.JoinLinkID().String(), ApplicationID: link.ApplicationID().String(),
		TokenHash: bson.Binary{Subtype: 0, Data: append([]byte{}, hash[:]...)}, Status: string(link.Status()),
		CreatedBy: link.CreatedBy().String(), CreatedAt: link.CreatedAt(), RevokedAt: link.RevokedAt(),
	}
	if v := link.RevokedBy(); v != nil {
		s := v.String()
		d.RevokedBy = &s
	}
	if v := link.RevocationReason(); v != nil {
		s := string(*v)
		d.RevocationReason = &s
	}
	if v := link.ReplacedByJoinLinkID(); v != nil {
		s := v.String()
		d.ReplacedByJoinLinkID = &s
	}
	return d
}

func testerJoinLinkFromDocument(d applicationTesterJoinLinkDocument) (*testerdomain.ApplicationTesterJoinLink, error) {
	if d.TokenHash.Subtype != 0 || len(d.TokenHash.Data) != 32 {
		return nil, fmt.Errorf("invalid tester join link hash shape")
	}
	var hash [32]byte
	copy(hash[:], d.TokenHash.Data)
	var revokedBy *shared.AuthID
	var reason *testerdomain.RevocationReason
	var replaced *testerdomain.ApplicationTesterJoinLinkID
	if d.RevokedBy != nil {
		v := shared.AuthID(*d.RevokedBy)
		revokedBy = &v
	}
	if d.RevocationReason != nil {
		v := testerdomain.RevocationReason(*d.RevocationReason)
		reason = &v
	}
	if d.ReplacedByJoinLinkID != nil {
		v := testerdomain.ApplicationTesterJoinLinkID(*d.ReplacedByJoinLinkID)
		replaced = &v
	}
	return testerdomain.RestoreTesterJoinLink(testerdomain.ApplicationTesterJoinLinkID(d.JoinLinkID), shared.ApplicationID(d.ApplicationID), testerdomain.NewTesterJoinTokenHash(hash), testerdomain.JoinLinkStatus(d.Status), shared.AuthID(d.CreatedBy), d.CreatedAt, revokedBy, d.RevokedAt, reason, replaced)
}

func (d applicationTesterJoinLinkDocument) String() string {
	return "applicationTesterJoinLinkDocument{credential: [redacted]}"
}
func (d applicationTesterJoinLinkDocument) GoString() string { return d.String() }
