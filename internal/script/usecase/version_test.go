package usecase

import (
	"errors"
	"testing"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

func TestBRSCR002DraftAuthorizationPrecedesValidation(t *testing.T) {
	h := NewDraftHandlers(nil, nil, nil)
	for _, tc := range []struct {
		identity shared.TrustedIdentity
		want     error
	}{
		{shared.TrustedIdentity{}, domain.ErrIdentityRequired},
		{shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{"app.script.review"}}, domain.ErrMaintainForbidden},
		{shared.TrustedIdentity{AuthID: "developer", DeveloperStatus: shared.DeveloperStatusApproved, Permissions: []string{"app.script.maintain.extra"}}, domain.ErrMaintainForbidden},
	} {
		_, createErr := h.Create(t.Context(), tc.identity, CreateDraftCommand{})
		_, replaceErr := h.Replace(t.Context(), tc.identity, ReplaceDraftCommand{})
		if !errors.Is(createErr, tc.want) || !errors.Is(replaceErr, tc.want) {
			t.Fatalf("create=%v replace=%v; want %v before parsing or storage", createErr, replaceErr, tc.want)
		}
	}
}
