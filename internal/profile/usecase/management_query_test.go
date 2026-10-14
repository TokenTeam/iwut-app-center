package usecase

import (
	"context"
	"errors"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"testing"
)

const managementTestApp = "01890a5d-ac96-774b-bcce-b302099a8057"
const managementTestRevision = "01890a5d-ac96-774b-bcce-b302099a8058"

type profileManagementRepoFake struct {
	calls  int
	caller shared.AuthID
	status *pd.ReviewStatus
	size   int32
	token  string
	err    error
	detail *pd.ManagedProfileDetail
}

func (f *profileManagementRepoFake) List(_ context.Context, caller shared.AuthID, _ shared.ApplicationID, status *pd.ReviewStatus, size int32, token string) (*pd.ManagedProfilePage, error) {
	f.calls++
	f.caller = caller
	f.status = status
	f.size = size
	f.token = token
	return &pd.ManagedProfilePage{Items: []pd.ManagedProfileRevision{}}, f.err
}
func (f *profileManagementRepoFake) Get(_ context.Context, caller shared.AuthID, _ shared.ApplicationID, _ pd.ApplicationProfileRevisionID, size int32, token string) (*pd.ManagedProfileDetail, error) {
	f.calls++
	f.caller = caller
	f.size = size
	f.token = token
	return f.detail, f.err
}

func TestProfileManagementQuery_BRPRF047050_ValidationAndAuthorizationOrdering(t *testing.T) {
	status := "REJECTED"
	for _, tc := range []struct {
		name   string
		caller string
		in     ListProfileRevisionsQuery
		want   error
		calls  int
	}{
		{"identity first", "", ListProfileRevisionsQuery{ApplicationID: "bad", PageSize: -1}, pd.ErrUserIdentityRequired, 0},
		{"invalid app", "admin", ListProfileRevisionsQuery{ApplicationID: "bad"}, pd.ErrInvalidProfileManagementQuery, 0},
		{"page too large", "admin", ListProfileRevisionsQuery{ApplicationID: managementTestApp, PageSize: 101}, pd.ErrInvalidProfileManagementQuery, 0},
		{"negative size", "admin", ListProfileRevisionsQuery{ApplicationID: managementTestApp, PageSize: -1}, pd.ErrInvalidProfileManagementQuery, 0},
		{"read needs only user", "admin", ListProfileRevisionsQuery{ApplicationID: managementTestApp, ReviewStatus: &status}, nil, 1},
		{"token deferred to owner snapshot", "admin", ListProfileRevisionsQuery{ApplicationID: managementTestApp, PageToken: string(make([]byte, 4097))}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &profileManagementRepoFake{}
			_, err := NewManagementQuery(f).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: shared.AuthID(tc.caller)}, tc.in)
			if !errors.Is(err, tc.want) || f.calls != tc.calls {
				t.Fatalf("error=%v calls=%d", err, f.calls)
			}
			if f.calls > 0 && f.size != 20 {
				t.Fatalf("default size=%d", f.size)
			}
		})
	}
	for _, status := range []string{"", "ALL", "DRAFT,APPROVED", "draft", "UNKNOWN"} {
		f := &profileManagementRepoFake{}
		_, err := NewManagementQuery(f).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, ListProfileRevisionsQuery{ApplicationID: managementTestApp, ReviewStatus: &status})
		if !errors.Is(err, pd.ErrInvalidProfileManagementQuery) || f.calls != 0 {
			t.Fatalf("filter %q error=%v calls=%d", status, err, f.calls)
		}
	}
}
func TestProfileManagementQuery_BRPRF047052_NotFoundAndNoPartialDetail(t *testing.T) {
	f := &profileManagementRepoFake{detail: &pd.ManagedProfileDetail{Profile: pd.ManagedProfileRevision{ApplicationID: managementTestApp, ProfileRevisionID: managementTestRevision}}}
	q := NewManagementQuery(f)
	identity := shared.AuthenticatedUserIdentity{AuthID: "admin"}
	if _, err := q.Get(t.Context(), identity, GetProfileRevisionQuery{ApplicationID: managementTestApp, ProfileRevisionID: "invalid"}); !errors.Is(err, pd.ErrProfileManagementNotFound) || f.calls != 0 {
		t.Fatal(err)
	}
	if _, err := q.Get(t.Context(), identity, GetProfileRevisionQuery{ApplicationID: managementTestApp, ProfileRevisionID: managementTestRevision}); err != nil || f.size != 20 {
		t.Fatal(err)
	}
	f.detail.Profile.ApplicationID = managementTestRevision
	if result, err := q.Get(t.Context(), identity, GetProfileRevisionQuery{ApplicationID: managementTestApp, ProfileRevisionID: managementTestRevision}); result != nil || !errors.Is(err, pd.ErrProfileManagementStateInconsistent) {
		t.Fatalf("result=%v error=%v", result, err)
	}
	for _, tc := range []struct{ source, want error }{
		{pp.ErrProfileManagementNotFound, pd.ErrProfileManagementNotFound},
		{pp.ErrInvalidProfileManagementPageToken, pd.ErrInvalidProfileManagementPageToken},
		{pp.ErrProfileManagementStateInconsistent, pd.ErrProfileManagementStateInconsistent},
		{pp.ErrProfileManagementResponseTooLarge, pd.ErrProfileManagementResponseTooLarge},
		{errors.New("private storage error"), pd.ErrProfileManagementUnavailable},
	} {
		f.err = tc.source
		result, err := q.List(t.Context(), identity, ListProfileRevisionsQuery{ApplicationID: managementTestApp})
		if result != nil || !errors.Is(err, tc.want) {
			t.Fatalf("result=%v error=%v", result, err)
		}
	}
}
