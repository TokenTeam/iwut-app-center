package mongo

import (
	"errors"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
)

func TestScriptQueryIntegration_UCAPP045_BRSCR014(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	writer, draft, command := newScriptSubmissionFixture(t, db)
	key, err := DeriveScriptQueryCursorKey([]byte("UC045 test cursor key"))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewScriptQueryRepository(db, key)
	ctx := t.Context()
	maintainer := port.ScriptReadScope{Actor: "maintainer", Maintain: true}
	reviewer := port.ScriptReadScope{Actor: "reviewer", Review: true}
	publisher := port.ScriptReadScope{Actor: "publisher", Publish: true}
	second, err := domain.NewDraft(domain.VersionID(nextIntegrationApplicationID(t)), draft.ResourceID, "release-2", draft.Content, draft.CreatedBy, draft.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.CreateDraft(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.SubmitDraft(ctx, command); err != nil {
		t.Fatal(err)
	}

	limit := port.ScriptPageRequest{Limit: 1}
	first, err := repo.ListVersions(ctx, maintainer, draft.ResourceID, "", limit)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first version page: %+v %v", first, err)
	}
	limit.Cursor = first.NextCursor
	next, err := repo.ListVersions(ctx, maintainer, draft.ResourceID, "", limit)
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" || first.Items[0].ID <= next.Items[0].ID ||
		!first.Items[0].CreatedAt.Equal(next.Items[0].CreatedAt) {
		t.Fatalf("second version page: %+v %v", next, err)
	}
	changed := maintainer
	changed.Review = true
	for _, scope := range []port.ScriptReadScope{changed, reviewer} {
		if _, err := repo.ListVersions(ctx, scope, draft.ResourceID, "", limit); !errors.Is(err, port.ErrInvalidScriptCursor) {
			t.Fatal("cursor accepted for a different read scope", err)
		}
	}
	if _, err := repo.ListVersions(ctx, maintainer, draft.ResourceID, domain.VersionDraft, limit); !errors.Is(err, port.ErrInvalidScriptCursor) {
		t.Fatal("cursor accepted for another filter", err)
	}
	if _, err := repo.ListResources(ctx, maintainer, limit); !errors.Is(err, port.ErrInvalidScriptCursor) {
		t.Fatal("cursor accepted for another list", err)
	}
	limit.Cursor += "a"
	if _, err := repo.ListVersions(ctx, maintainer, draft.ResourceID, "", limit); !errors.Is(err, port.ErrInvalidScriptCursor) {
		t.Fatal("tampered cursor accepted", err)
	}
	if _, err := repo.GetVersion(ctx, reviewer, draft.ResourceID, second.ID); !errors.Is(err, domain.ErrVersionNotFound) {
		t.Fatal("reviewer can locate unrelated draft", err)
	}
	if view, err := repo.GetVersion(ctx, publisher, draft.ResourceID, draft.ID); err != nil || view.Text != nil || view.ReviewID != command.ReviewID {
		t.Fatalf("publisher summary: %+v %v", view, err)
	}
	pending, err := repo.ListPendingReviews(ctx, reviewer, draft.ResourceID, port.ScriptPageRequest{Limit: 20})
	if err != nil || len(pending.Items) != 1 || pending.Items[0].ID != command.ReviewID {
		t.Fatalf("pending queue: %+v %v", pending, err)
	}

	if _, err := NewScriptResourceRepository(db).ReplaceResourceMetadata(ctx, port.ReplaceMetadata{
		ID: draft.ResourceID, ExpectedRevision: 1, Actor: "resource-owner", At: command.At,
		Metadata: domain.Metadata{Name: "New name", Purpose: "New purpose"},
	}); err != nil {
		t.Fatal(err)
	}
	detail, err := repo.GetReview(ctx, draft.ResourceID, draft.ID, command.ReviewID)
	if err != nil || detail.Version.Revision != 2 || detail.Review.Snapshot.Content != draft.Content ||
		detail.Review.Snapshot.ResourceMetadata.Name != "Original name" ||
		!slices.Equal(detail.Review.Policy.Checks, domain.InitialReviewPolicy().Checks) {
		t.Fatalf("review detail lost bound facts: %v", err)
	}
	if _, err := db.Collection(scriptReviewsCollection).UpdateOne(ctx, bson.M{"reviewId": command.ReviewID},
		bson.M{"$set": bson.M{"snapshot.digest": second.Content.Digest[1:] + "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetReview(ctx, draft.ResourceID, draft.ID, command.ReviewID); !errors.Is(err, port.ErrScriptQueryInconsistent) {
		t.Fatal("corrupt digest accepted by detail", err)
	}
	if _, err := repo.ListPendingReviews(ctx, reviewer, "", port.ScriptPageRequest{Limit: 20}); !errors.Is(err, port.ErrScriptQueryInconsistent) {
		t.Fatal("corrupt digest accepted by queue", err)
	}
}
