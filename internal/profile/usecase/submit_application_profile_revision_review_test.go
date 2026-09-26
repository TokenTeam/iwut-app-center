package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"testing"
	"time"
)

type reviewIDs struct {
	calls int
	err   error
	id    domain.ApplicationProfileReviewID
}

func (i *reviewIDs) NewUUIDv7() (domain.ApplicationProfileReviewID, error) {
	i.calls++
	return i.id, i.err
}

type submitRepo struct {
	calls     int
	err       error
	nilResult bool
	app       shared.ApplicationID
	id        domain.ApplicationProfileRevisionID
	actor     shared.AuthID
	expected  int64
	reviewID  domain.ApplicationProfileReviewID
	at        time.Time
}

func (r *submitRepo) SubmitDraft(_ context.Context, app shared.ApplicationID, id domain.ApplicationProfileRevisionID, actor shared.AuthID, expected int64, reviewID domain.ApplicationProfileReviewID, at time.Time) (*domain.ApplicationProfileSubmission, error) {
	r.calls++
	r.app = app
	r.id = id
	r.actor = actor
	r.expected = expected
	r.reviewID = reviewID
	r.at = at
	if r.err != nil || r.nilResult {
		return nil, r.err
	}
	name, _ := domain.NewApplicationDisplayName("name")
	d, _ := domain.NewDraftApplicationProfileRevision(id, app, name, nil, nil, "creator", time.Unix(10, 0))
	v, _ := d.AssignSequence(1)
	return v.SubmitDraft(expected, reviewID, 1, actor, at)
}
func submitCommand() SubmitApplicationProfileRevisionReviewCommand {
	return SubmitApplicationProfileRevisionReviewCommand{appID, profileID, 1}
}
func TestBRPRF015019020SubmissionUseCase(t *testing.T) {
	_, clock, _ := dependencies()
	ids := &reviewIDs{id: "01995000-0000-7000-8000-000000000015"}
	repo := &submitRepo{}
	got, err := NewSubmitApplicationProfileRevisionReviewHandler(ids, clock, repo).Handle(t.Context(), approved(), submitCommand())
	if err != nil || got.ProfileRevision.Revision() != 2 || got.Review.SubmittedBy() != "admin" || ids.calls != 1 || clock.calls != 1 || repo.calls != 1 || repo.app != appID || repo.id != profileID || repo.expected != 1 || repo.reviewID != ids.id || !repo.at.Equal(clock.at) {
		t.Fatalf("%v %v", got, err)
	}
}
func TestBRPRF015016SubmissionValidationAndDependencies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity DeveloperIdentity
		c        SubmitApplicationProfileRevisionReviewCommand
		want     error
	}{
		{"identity", DeveloperIdentity{}, submitCommand(), domain.ErrDeveloperIdentityRequired},
		{"approval", DeveloperIdentity{AuthID: "admin"}, submitCommand(), domain.ErrDeveloperApprovalRequired},
		{"app", approved(), SubmitApplicationProfileRevisionReviewCommand{"bad", profileID, 1}, domain.ErrInvalidApplicationProfileReviewSubmission},
		{"id", approved(), SubmitApplicationProfileRevisionReviewCommand{appID, "bad", 1}, domain.ErrInvalidApplicationProfileReviewSubmission},
		{"revision", approved(), SubmitApplicationProfileRevisionReviewCommand{appID, profileID, 0}, domain.ErrInvalidApplicationProfileReviewSubmission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, clock, _ := dependencies()
			ids := &reviewIDs{id: "01995000-0000-7000-8000-000000000015"}
			repo := &submitRepo{}
			_, err := NewSubmitApplicationProfileRevisionReviewHandler(ids, clock, repo).Handle(t.Context(), tc.identity, tc.c)
			if !errors.Is(err, tc.want) || ids.calls != 0 || clock.calls != 0 || repo.calls != 0 {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		name      string
		id        domain.ApplicationProfileReviewID
		err       error
		zeroClock bool
	}{{"id failure", "", errors.New("failure"), false}, {"bad ID", "bad", nil, false}, {"clock", "01995000-0000-7000-8000-000000000015", nil, true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, clock, _ := dependencies()
			if tc.zeroClock {
				clock.at = time.Time{}
			}
			repo := &submitRepo{}
			_, err := NewSubmitApplicationProfileRevisionReviewHandler(&reviewIDs{id: tc.id, err: tc.err}, clock, repo).Handle(t.Context(), approved(), submitCommand())
			if !errors.Is(err, domain.ErrInternal) || repo.calls != 0 {
				t.Fatal(err)
			}
		})
	}
}
func TestBRPRF021SubmissionErrorMapping(t *testing.T) {
	for _, tc := range []struct{ from, to error }{{port.ErrApplicationProfileRevisionNotFound, domain.ErrApplicationProfileRevisionNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired}, {port.ErrApplicationProfileRevisionNotDraft, domain.ErrApplicationProfileRevisionNotDraft}, {port.ErrApplicationProfileRevisionConflict, domain.ErrApplicationProfileRevisionConflict}, {port.ErrInvalidApplicationProfileContent, domain.ErrInvalidApplicationProfileContent}, {port.ErrApplicationProfileStateInconsistent, domain.ErrApplicationProfileStateInconsistent}, {errors.New("db secret"), domain.ErrInternal}, {nil, domain.ErrInternal}} {
		_, clock, _ := dependencies()
		repo := &submitRepo{err: tc.from, nilResult: true}
		_, err := NewSubmitApplicationProfileRevisionReviewHandler(&reviewIDs{id: "01995000-0000-7000-8000-000000000015"}, clock, repo).Handle(t.Context(), approved(), submitCommand())
		if !errors.Is(err, tc.to) {
			t.Fatal(err)
		}
	}
}
