package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
)

type decisionRepositoryProbe struct {
	calls  int
	input  port.ProfileReviewDecisionInput
	result *domain.ApplicationProfileDecisionResult
	err    error
}

func (r *decisionRepositoryProbe) DecideReview(_ context.Context, input port.ProfileReviewDecisionInput) (*domain.ApplicationProfileDecisionResult, error) {
	r.calls++
	r.input = input
	return r.result, r.err
}

func profileDecisionCommand() DecideApplicationProfileRevisionReviewCommand {
	return DecideApplicationProfileRevisionReviewCommand{
		ApplicationID: appID, ProfileRevisionID: profileID,
		ProfileReviewID:                 "01995000-0000-7000-8000-000000000016",
		ExpectedProfileRevisionRevision: 2, PublicationPreconditionPresent: true,
		ExpectedPolicyVersion: domain.InitialProfileReviewPolicyVersion, Outcome: "APPROVE",
		ConfirmedCheckIDs: domain.InitialProfileReviewChecks(),
	}
}

func profileReviewer() ReviewerIdentity {
	return ReviewerIdentity{AuthID: "reviewer", Permissions: []string{domain.ProfileReviewPermission}}
}

func profileDecisionFixture(t *testing.T) *domain.ApplicationProfileDecisionResult {
	t.Helper()
	name, err := domain.NewApplicationDisplayName("Profile")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := domain.NewDraftApplicationProfileRevision(profileID, appID, name, nil, nil, "creator", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := draft.AssignSequence(1)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := revision.SubmitDraft(1, domain.ApplicationProfileReviewID(profileDecisionCommand().ProfileReviewID), 1, "submitter", time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	result, err := submission.ProfileRevision.DecideReview(submission.Review, "reviewer", "admin", []string{domain.ProfileReviewPermission}, 2, "APPROVE", domain.ProfileReviewPolicy{Version: domain.InitialProfileReviewPolicyVersion, RequiredChecks: domain.InitialProfileReviewChecks(), Status: "ACTIVE"}, domain.InitialProfileReviewChecks(), nil, time.Unix(3, 0))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBRPRF023027030_DecisionRejectsBeforeClockOrPersistence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ReviewerIdentity, *DecideApplicationProfileRevisionReviewCommand)
		want   error
	}{
		{"missing identity", func(i *ReviewerIdentity, _ *DecideApplicationProfileRevisionReviewCommand) { i.AuthID = "" }, domain.ErrReviewerIdentityRequired},
		{"missing permission", func(i *ReviewerIdentity, _ *DecideApplicationProfileRevisionReviewCommand) { i.Permissions = nil }, domain.ErrApplicationProfileReviewPermissionRequired},
		{"version permission is independent", func(i *ReviewerIdentity, _ *DecideApplicationProfileRevisionReviewCommand) {
			i.Permissions = []string{"app.version.review"}
		}, domain.ErrApplicationProfileReviewPermissionRequired},
		{"invalid application", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.ApplicationID = "invalid"
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"invalid revision ID", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.ProfileRevisionID = "invalid"
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"invalid review ID", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.ProfileReviewID = "invalid"
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"nonpositive revision", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.ExpectedProfileRevisionRevision = 0
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"unknown outcome", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) { c.Outcome = "APPROVED" }, domain.ErrInvalidApplicationProfileReviewDecision},
		{"invalid policy", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.ExpectedPolicyVersion = " policy"
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"missing publication precondition", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.PublicationPreconditionPresent = false
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"invalid publication ID", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			value := "invalid"
			c.ExpectedCurrentPublishedProfileRevisionID = &value
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"explicit empty reason", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			value := ""
			c.Reason = &value
		}, domain.ErrInvalidProfileReviewReason},
		{"reject needs reason", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.Outcome = "REJECT"
			c.PublicationPreconditionPresent = false
			c.ConfirmedCheckIDs = nil
		}, domain.ErrInvalidProfileReviewReason},
		{"reject forbids publication precondition", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.Outcome = "REJECT"
			value := "reason"
			c.Reason = &value
			c.ConfirmedCheckIDs = nil
		}, domain.ErrInvalidApplicationProfileReviewDecision},
		{"reject forbids checks", func(_ *ReviewerIdentity, c *DecideApplicationProfileRevisionReviewCommand) {
			c.Outcome = "REJECT"
			c.PublicationPreconditionPresent = false
			value := "reason"
			c.Reason = &value
		}, domain.ErrProfileReviewChecksIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, command := profileReviewer(), profileDecisionCommand()
			tc.change(&identity, &command)
			_, clock, _ := dependencies()
			repo := &decisionRepositoryProbe{}
			got, err := NewDecideApplicationProfileRevisionReviewHandler(clock, repo).Handle(t.Context(), identity, command)
			if got != nil || !errors.Is(err, tc.want) || clock.calls != 0 || repo.calls != 0 {
				t.Fatalf("result=%v error=%v clock=%d repository=%d", got, err, clock.calls, repo.calls)
			}
		})
	}
}

func TestBRPRF023027030032_DecisionTransfersTrustedFactsAndPreconditions(t *testing.T) {
	for _, mode := range []string{"approve NONE", "approve existing", "reject verbatim reason"} {
		t.Run(mode, func(t *testing.T) {
			identity, command := profileReviewer(), profileDecisionCommand()
			identity.Permissions = append(identity.Permissions, "unrelated.permission")
			reason := "Cafe\u0301 remains verbatim"
			command.Reason = &reason
			if mode == "approve existing" {
				value := "01995000-0000-7000-8000-000000000020"
				command.ExpectedCurrentPublishedProfileRevisionID = &value
			}
			if mode == "reject verbatim reason" {
				command.Outcome = "REJECT"
				command.PublicationPreconditionPresent = false
				command.ConfirmedCheckIDs = nil
			}
			_, clock, _ := dependencies()
			clock.at = time.Date(2026, 9, 27, 12, 0, 0, 123456789, time.FixedZone("+08", 8*3600))
			repo := &decisionRepositoryProbe{result: profileDecisionFixture(t)}
			got, err := NewDecideApplicationProfileRevisionReviewHandler(clock, repo).Handle(t.Context(), identity, command)
			if err != nil || got != repo.result || clock.calls != 1 || repo.calls != 1 {
				t.Fatalf("result=%v error=%v", got, err)
			}
			in := repo.input
			if in.ApplicationID.String() != command.ApplicationID || in.ProfileRevisionID.String() != command.ProfileRevisionID || in.ProfileReviewID.String() != command.ProfileReviewID || in.ReviewerID != identity.AuthID || in.ExpectedRevision != 2 || in.PolicyVersion != command.ExpectedPolicyVersion || in.Outcome != command.Outcome || in.Reason == nil || *in.Reason != reason || !in.DecidedAt.Equal(clock.at) || in.DecidedAt.Location() != time.UTC {
				t.Fatalf("incorrect atomic input: %+v", in)
			}
			if command.ExpectedCurrentPublishedProfileRevisionID == nil {
				if in.ExpectedPublishedID != nil {
					t.Fatal("NONE changed")
				}
			} else if in.ExpectedPublishedID == nil || in.ExpectedPublishedID.String() != *command.ExpectedCurrentPublishedProfileRevisionID {
				t.Fatal("publication precondition changed")
			}
			identity.Permissions[0] = "changed after call"
			if repo.input.Permissions[0] != domain.ProfileReviewPermission {
				t.Fatal("permission slice aliases caller")
			}
			if len(command.ConfirmedCheckIDs) != 0 {
				command.ConfirmedCheckIDs[0] = "changed after call"
				if repo.input.ConfirmedCheckIDs[0] != "content-policy-reviewed" {
					t.Fatal("checks slice aliases caller")
				}
			}
		})
	}
}

func TestBRPRF024032_DecisionDependencyAndFailureBoundary(t *testing.T) {
	for _, cause := range []error{
		domain.ErrApplicationProfileReviewConflictOfInterest, domain.ErrApplicationProfileReviewNotFound,
		domain.ErrApplicationProfileReviewAlreadyDecided, domain.ErrApplicationProfileReviewStateConflict,
		domain.ErrApplicationProfileRevisionConflict, domain.ErrApplicationProfilePublicationConflict,
		domain.ErrApplicationProfileReviewStateInconsistent, domain.ErrProfileReviewPolicyUnavailable,
		domain.ErrProfileReviewChecksIncomplete, domain.ErrInvalidApplicationProfileContent,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			_, clock, _ := dependencies()
			repo := &decisionRepositoryProbe{err: fmt.Errorf("atomic operation: %w", cause)}
			result, err := NewDecideApplicationProfileRevisionReviewHandler(clock, repo).Handle(t.Context(), profileReviewer(), profileDecisionCommand())
			if result != nil || !errors.Is(err, cause) {
				t.Fatalf("result=%v error=%v", result, err)
			}
		})
	}
	for _, mode := range []string{"nil handler", "nil clock", "zero time", "nil repository", "storage failure", "nil result", "missing revision", "missing review"} {
		t.Run(mode, func(t *testing.T) {
			_, clock, _ := dependencies()
			repo := &decisionRepositoryProbe{result: profileDecisionFixture(t)}
			handler := NewDecideApplicationProfileRevisionReviewHandler(clock, repo)
			wantCalls := 0
			switch mode {
			case "nil handler":
				handler = nil
			case "nil clock":
				handler.clock = nil
			case "zero time":
				clock.at = time.Time{}
			case "nil repository":
				handler.repository = nil
			case "storage failure":
				repo.err = errors.New("private database details")
				wantCalls = 1
			case "nil result":
				repo.result = nil
				wantCalls = 1
			case "missing revision":
				repo.result.ProfileRevision = nil
				wantCalls = 1
			case "missing review":
				repo.result.Review = nil
				wantCalls = 1
			}
			result, err := handler.Handle(t.Context(), profileReviewer(), profileDecisionCommand())
			if result != nil || !errors.Is(err, domain.ErrInternal) || strings.Contains(err.Error(), "private") || repo.calls != wantCalls {
				t.Fatalf("result=%v error=%v calls=%d", result, err, repo.calls)
			}
		})
	}
}
