package mongo

import (
	"errors"
	"testing"
	"time"

	exit "iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func TestScriptAuthoringIntegration_BRAPP008_BRSCR002(t *testing.T) {
	ctx := t.Context()
	db := migratedIntegrationDatabase(t, integrationClient(t))
	resources := NewScriptResourceRepository(db)
	versions, draft, submit := newScriptSubmissionFixture(t, db)
	owner := draft.CreatedBy
	queries := NewScriptQueryRepository(db, ScriptQueryCursorKey{1})
	page := port.ScriptPageRequest{Limit: 1}
	got, err := queries.ListOwnResources(ctx, owner, page)
	if err != nil || len(got.Items) != 1 || got.Items[0].LatestVersion == nil || got.Items[0].LatestVersion.ID != draft.ID {
		t.Fatalf("owner draft resource projection: %#v %v", got, err)
	}
	if got, err := queries.ListOwnResources(ctx, "other-author", page); err != nil || len(got.Items) != 0 {
		t.Fatalf("private resource leaked into another author's list: %#v %v", got, err)
	}
	if got, err := queries.ListResources(ctx, port.ScriptReadScope{Actor: "reviewer", Review: true, Publish: true, Suspend: true, Restore: true}, page); err != nil || len(got.Items) != 0 {
		t.Fatalf("draft-only resource leaked into governance: %#v %v", got, err)
	}
	edit := port.ReplaceDraft{ResourceID: draft.ResourceID, VersionID: draft.ID, ExpectedRevision: 1, Content: draft.Content, Actor: "other-author", At: submit.At}
	if _, err := versions.ReplaceDraft(ctx, edit); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-owner draft update: %v", err)
	}
	crossSubmit := submit
	crossSubmit.Actor = edit.Actor
	if _, err := versions.SubmitDraft(ctx, crossSubmit); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-owner submission: %v", err)
	}
	if _, err := queries.GetVersion(ctx, port.ScriptReadScope{Actor: edit.Actor, Own: true}, draft.ResourceID, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-owner draft read: %v", err)
	}

	// The initial writes committed before Prepare. Script ownership leaves the
	// account eligible to exit, and Prepare must exclude every later write.
	ownerExit := NewAccountOwnerExitRepository(db)
	prepare := exitPrepare(owner.String(), exit.Withdrawal)
	now := time.Now().UTC()
	receipt := exitReceipt(t)
	prepared, err := ownerExit.Prepare(ctx, prepare, receipt, now)
	if err != nil || prepared.Blocked || prepared.ReceiptID != receipt {
		t.Fatalf("scripts should not block owner exit: %#v %v", prepared, err)
	}
	metadata := domain.Metadata{Name: "New name", Purpose: "New purpose"}
	resource, err := domain.NewResource(domain.ResourceID(nextIntegrationApplicationID(t)), metadata, owner, now)
	if err != nil {
		t.Fatal(err)
	}
	newDraft, err := domain.NewDraft(domain.VersionID(nextIntegrationApplicationID(t)), draft.ResourceID, "new-draft", draft.Content, owner, now)
	if err != nil {
		t.Fatal(err)
	}
	edit.Actor = owner
	update := port.ReplaceMetadata{ID: draft.ResourceID, ExpectedRevision: 1, Metadata: metadata, Actor: owner, At: now}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"create resource", func() error { _, err := resources.CreateResource(ctx, resource); return err }},
		{"update resource", func() error { _, err := resources.ReplaceResourceMetadata(ctx, update); return err }},
		{"create draft", func() error { _, err := versions.CreateDraft(ctx, newDraft); return err }},
		{"edit draft", func() error { _, err := versions.ReplaceDraft(ctx, edit); return err }},
		{"submit draft", func() error { _, err := versions.SubmitDraft(ctx, submit); return err }},
	} {
		if err := operation.run(); !errors.Is(err, shared.ErrAccountExitBlocked) {
			t.Fatalf("%s during exit Prepare: %v", operation.name, err)
		}
	}
	assertCollectionCount(t, db, scriptResourcesCollectionName, 1)
	assertCollectionCount(t, db, scriptResourceAuditsCollectionName, 1)
	assertCollectionCount(t, db, scriptVersionAuditsCollection, 1)
	assertCollectionCount(t, db, scriptReviewsCollection, 0)

	if _, err := ownerExit.Finish(ctx, exit.Finish{Prepare: prepare, ReceiptID: receipt, Decision: exit.Cancelled}, now); err != nil {
		t.Fatal(err)
	}
	submission, err := versions.SubmitDraft(ctx, submit)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := queries.GetOwnReview(ctx, owner, draft.ResourceID, draft.ID, submission.ReviewID); err != nil || got.Status != domain.ReviewPending {
		t.Fatalf("owner result after submission: %#v %v", got, err)
	}
	prepare = exitPrepare(owner.String(), exit.Withdrawal)
	receipt = exitReceipt(t)
	if _, err := ownerExit.Prepare(ctx, prepare, receipt, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerExit.Finish(ctx, exit.Finish{Prepare: prepare, ReceiptID: receipt, Decision: exit.Committed}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.CreateResource(ctx, resource); !errors.Is(err, shared.ErrAccountExitBlocked) {
		t.Fatalf("sealed account could create script: %v", err)
	}
}
