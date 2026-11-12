package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
	"strings"
	"testing"
	"time"
)

type publicationIDStub struct{}

func (publicationIDStub) NewUUIDv7() (domain.PublicationEventID, error) {
	return "018f0000-0000-7000-8000-000000000046", nil
}

type publicationRepositoryStub struct {
	calls   int
	command port.PublicationCommand
}

func (r *publicationRepositoryStub) ApplyPublication(_ context.Context, command port.PublicationCommand) (domain.PublicationResult, error) {
	r.calls++
	r.command = command
	return domain.PublicationResult{}, nil
}

func TestBRSCR002018PublicationRequiresIndependentPermissionBeforeLookup(t *testing.T) {
	for _, action := range []string{domain.PublishScript, domain.ClearScriptHead, domain.SuspendScript, domain.RestoreScript} {
		for _, permission := range []string{"app.script.review", "app.script.publish", "app.script.suspend", "app.script.restore", "app.version.review", "auth.platform-admin.manage"} {
			identity := shared.TrustedIdentity{AuthID: "actor", Permissions: []string{permission}}
			err := RequirePublicationPermission(identity, action)
			if (err == nil) != (permission == domain.PublicationPermission(action)) {
				t.Fatalf("%s with %s: %v", action, permission, err)
			}
		}
	}
	repo := &publicationRepositoryStub{}
	handler := NewPublicationHandler(publicationIDStub{}, resourceClock(time.Now()), repo)
	if _, err := handler.Apply(t.Context(), shared.TrustedIdentity{AuthID: "actor", DeveloperStatus: shared.DeveloperStatusApproved}, PublicationCommand{ResourceID: "invalid", Action: domain.PublishScript}); !errors.Is(err, domain.ErrPublicationForbidden) || repo.calls != 0 {
		t.Fatal("looked up unauthorized target", err)
	}
	if err := RequirePublicationPermission(shared.TrustedIdentity{Permissions: []string{"app.script.publish"}}, domain.PublishScript); !errors.Is(err, domain.ErrIdentityRequired) {
		t.Fatal(err)
	}
}

func TestBRSCR018DistributionReasonUsesTrimmedUTF8Bytes(t *testing.T) {
	repo := &publicationRepositoryStub{}
	handler := NewPublicationHandler(publicationIDStub{}, resourceClock(time.Now()), repo)
	identity := shared.TrustedIdentity{AuthID: "operator", Permissions: []string{"app.script.suspend"}}
	command := PublicationCommand{ResourceID: string(testResourceID), Action: domain.SuspendScript, ExpectedRevision: 1, Reason: "  " + strings.Repeat("中", 682) + "ab  "}
	if _, err := handler.Apply(t.Context(), identity, command); err != nil || len(repo.command.Reason) != 2048 {
		t.Fatal("2048 byte reason", err)
	}
	command.Reason += "x"
	if _, err := handler.Apply(t.Context(), identity, command); !errors.Is(err, domain.ErrInvalidPublicationRequest) {
		t.Fatal("overlong reason", err)
	}
	command.Reason = " \t "
	if _, err := handler.Apply(t.Context(), identity, command); !errors.Is(err, domain.ErrInvalidPublicationRequest) {
		t.Fatal("blank reason", err)
	}
}
