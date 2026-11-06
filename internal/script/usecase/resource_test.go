package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const testResourceID domain.ResourceID = "018f0000-0000-7000-8000-000000000041"

type fixedResourceID struct{}

func (fixedResourceID) NewUUIDv7() (domain.ResourceID, error) { return testResourceID, nil }

type resourceClock time.Time

func (c resourceClock) Now() time.Time { return time.Time(c) }

type resourceRepositoryStub struct {
	creates  []domain.Resource
	replaces []port.ReplaceMetadata
	err      error
}

func (r *resourceRepositoryStub) CreateResource(_ context.Context, resource domain.Resource) (domain.Resource, error) {
	r.creates = append(r.creates, resource)
	return resource, r.err
}

func (r *resourceRepositoryStub) ReplaceResourceMetadata(_ context.Context, input port.ReplaceMetadata) (domain.Resource, error) {
	r.replaces = append(r.replaces, input)
	return domain.Resource{}, r.err
}

func TestBRSCR002ResourceRequiresExactPermissionBeforeValidationOrLookup(t *testing.T) {
	at := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	repo := &resourceRepositoryStub{}
	h := NewResourceHandlers(fixedResourceID{}, resourceClock(at), repo)
	for _, test := range []struct {
		name     string
		identity shared.TrustedIdentity
		want     error
	}{
		{"missing", shared.TrustedIdentity{Permissions: []string{domain.MaintainPermission}}, domain.ErrIdentityRequired},
		{"developer reviewer and admin", shared.TrustedIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved, Permissions: []string{"app.version.review", "auth.platform-admin.manage"}}, domain.ErrMaintainForbidden},
		{"wildcard and lookalike", shared.TrustedIdentity{AuthID: "user", Permissions: []string{"app.script.*", "app.script.maintain.extra", "app.script.Maintain"}}, domain.ErrMaintainForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, createErr := h.Create(t.Context(), test.identity, CreateResourceCommand{})
			_, replaceErr := h.ReplaceMetadata(t.Context(), test.identity, ReplaceResourceMetadataCommand{ResourceID: "invalid"})
			if !errors.Is(createErr, test.want) || !errors.Is(replaceErr, test.want) {
				t.Fatalf("create=%v replace=%v; want %v", createErr, replaceErr, test.want)
			}
			if len(repo.creates)+len(repo.replaces) != 0 {
				t.Fatal("unauthorized caller reached persistence")
			}
		})
	}
	maintainer := shared.TrustedIdentity{AuthID: "script-maintainer", Permissions: []string{domain.MaintainPermission}}
	got, err := h.Create(t.Context(), maintainer, CreateResourceCommand{Name: "  Campus  ", Purpose: " Collect course metadata. "})
	if err != nil || got.ID != testResourceID || got.Revision != 1 || got.CreatedBy != maintainer.AuthID ||
		got.UpdatedBy != maintainer.AuthID || !got.CreatedAt.Equal(at) || !got.UpdatedAt.Equal(at) ||
		got.Metadata.Name != "Campus" || got.Metadata.Purpose != " Collect course metadata. " {
		t.Fatalf("maintainer without developer status: %#v, %v", got, err)
	}
	repo.err = domain.ErrRevisionConflict
	_, err = h.ReplaceMetadata(t.Context(), maintainer, ReplaceResourceMetadataCommand{
		ResourceID: string(testResourceID), ExpectedRevision: 1, Name: "Updated", Purpose: "Purpose",
	})
	if !errors.Is(err, domain.ErrRevisionConflict) || len(repo.replaces) != 1 ||
		repo.replaces[0].Actor != maintainer.AuthID || !repo.replaces[0].At.Equal(at) {
		t.Fatalf("replace did not preserve conflict or trusted audit: %#v, %v", repo.replaces, err)
	}
}
