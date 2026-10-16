package transport

import (
	q "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review_query"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pd "iwut-app-center/internal/profile/domain"
	"strings"
	"testing"
)

func TestBR_PRF_043_046_PublishedProjectionPresenceAndBudget(t *testing.T) {
	for _, v := range []*pd.CurrentPublishedProfileForReview{nil, {}, {None: true, Published: &pd.PublishedProfileForReview{}}, {Published: &pd.PublishedProfileForReview{ProfileRevisionID: "bad", DisplayName: "ok"}}} {
		if _, err := publishedProfileForReviewResponse(v); err == nil {
			t.Fatal("invalid projection accepted")
		}
	}
	none, err := publishedProfileForReviewResponse(&pd.CurrentPublishedProfileForReview{None: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := protojson.Marshal(none)
	if err != nil || string(b) != "{\"none\":null}" {
		t.Fatalf("NONE wire = %s, %v", b, err)
	}
	desc, icon := strings.Repeat("界", 1000), strings.Repeat("界", 512)
	v := &pd.CurrentPublishedProfileForReview{Published: &pd.PublishedProfileForReview{ProfileRevisionID: "01995000-0000-7000-8000-000000000002", DisplayName: strings.Repeat("界", 80), Description: &desc, Icon: &icon}}
	out, err := publishedProfileForReviewResponse(v)
	if err != nil {
		t.Fatal(err)
	}
	b, err = protojson.Marshal(out)
	if err != nil || len(b) >= 32*1024 || proto.Size(out) >= 32*1024 {
		t.Fatal("projection response budget", err)
	}
	encoded, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var round q.CurrentPublishedProfileForReview
	if err = proto.Unmarshal(encoded, &round); err != nil || round.GetPublished().GetDescription() != desc || round.GetPublished().GetIcon() != icon {
		t.Fatal("content/presence lost", err)
	}
}
