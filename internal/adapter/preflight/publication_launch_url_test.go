package preflight

import (
	"context"
	"errors"
	publicationport "iwut-app-center/internal/publication/port"
	"net/netip"
	"testing"
)

func TestPublicationLaunchURL_BRPUB008PolicyAndErrorMapping(t *testing.T) {
	resolver := &deterministicResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
	policy := NewPublicationLaunchURLSubmissionPolicy(NewLaunchURLSubmissionPolicy(resolver))
	version, err := policy.Inspect(context.Background(), "https://app.iwut.net/app")
	if err != nil || version.String() != PolicyVersion {
		t.Fatalf("%v %v", version, err)
	}
	resolver.addresses = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if _, err = policy.Inspect(context.Background(), "https://app.iwut.net/app"); !errors.Is(err, publicationport.ErrLaunchURLNotReviewable) {
		t.Fatal(err)
	}
	resolver.err = errors.New("DNS unavailable")
	if _, err = policy.Inspect(context.Background(), "https://app.iwut.net/app"); !errors.Is(err, publicationport.ErrLaunchURLInspectionUnavailable) {
		t.Fatal(err)
	}
}
