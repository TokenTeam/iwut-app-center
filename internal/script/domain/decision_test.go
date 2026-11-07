package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"strings"
	"testing"
	"time"
)

func TestBRSCR010011DecisionContributorsAndReasonBytes(t *testing.T) {
	at := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	resource, err := NewResource("018f0000-0000-7000-8000-000000000041", Metadata{Name: "Script", Purpose: "Test"}, "resource-owner", at)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := NewContent(JSONFormat, "{}")
	version, err := NewDraft("018f0000-0000-7000-8000-000000000042", resource.ID, "v1", content, "author", at)
	if err != nil {
		t.Fatal(err)
	}
	edited, _ := NewContent(JSONFormat, "{ }")
	version, _, err = version.ReplaceDraft(1, edited, "editor", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	version, review, err := version.Submit(2, resource, "018f0000-0000-7000-8000-000000000043", InitialReviewPolicy(), "submitter", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	input := DecisionInput{ExpectedRevision: 3, PolicyVersion: InitialPolicyVersion, Action: "REJECT", Reason: strings.Repeat("中", 682) + "ab"}
	decide := func(actor shared.AuthID, input DecisionInput) error {
		_, _, err := review.Decide(version, input, "018f0000-0000-7000-8000-000000000044", actor, at.Add(3*time.Second))
		return err
	}
	for _, actor := range []shared.AuthID{"author", "editor", "submitter"} {
		if err := decide(actor, input); !errors.Is(err, ErrReviewConflict) {
			t.Fatalf("conflict %s: %v", actor, err)
		}
	}
	if err := decide("reviewer", input); err != nil {
		t.Fatalf("2048 bytes: %v", err)
	}
	input.Reason += "x"
	if err := decide("reviewer", input); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("2049 bytes: %v", err)
	}
	input.Reason = " \t "
	if err := decide("reviewer", input); !errors.Is(err, ErrInvalidDecision) {
		t.Fatal("blank reason", err)
	}
	input.Reason = "reason"
	input.ConfirmedCheckIDs = []string{"declared-purpose"}
	if err := decide("reviewer", input); !errors.Is(err, ErrInvalidDecision) {
		t.Fatal("rejection with checks", err)
	}
}
