package usecase

import (
	"context"
	"errors"
	cd "iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/shared"
	"reflect"
	"strings"
	"testing"
)

type myTestFake struct {
	caller      shared.AuthID
	major, size int32
	host        []cd.CapabilityName
	err         error
	calls       int
}

func (f *myTestFake) ListMyTest(_ context.Context, c shared.AuthID, m int32, h []cd.CapabilityName, s int32, _ string) (*cd.MyTestApplicationPage, error) {
	f.calls++
	f.caller = c
	f.major = m
	f.host = h
	f.size = s
	return &cd.MyTestApplicationPage{}, f.err
}
func (f *myTestFake) GetMyTest(_ context.Context, a shared.ApplicationID, c shared.AuthID, m int32, h []cd.CapabilityName) (*cd.MyTestApplicationDetail, error) {
	f.calls++
	f.caller = c
	f.major = m
	f.host = h
	return &cd.MyTestApplicationDetail{Item: &cd.MyTestApplicationItem{ApplicationID: a}}, f.err
}
func TestBR_CAT_011_015_MyTestQueryIdentityValidationNormalization(t *testing.T) {
	f := &myTestFake{}
	handler := NewMyTestApplications(f)
	identity := shared.AuthenticatedUserIdentity{AuthID: "opaque-user"}
	_, err := handler.List(t.Context(), identity, ListMyTestApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 3, HostCapabilities: []string{"z.v1", "a.v2", "z.v1"}}})
	if err != nil || f.caller != identity.AuthID || f.size != 20 || !reflect.DeepEqual(f.host, []cd.CapabilityName{"a.v2", "z.v1"}) {
		t.Fatal(f, err)
	}
	for _, tc := range []struct {
		name     string
		identity shared.AuthenticatedUserIdentity
		q        ListMyTestApplicationsQuery
		want     error
	}{
		{"identity first", shared.AuthenticatedUserIdentity{}, ListMyTestApplicationsQuery{}, cd.ErrAuthenticatedUserRequired},
		{"major", identity, ListMyTestApplicationsQuery{}, cd.ErrInvalidMyTestApplicationQuery},
		{"capability", identity, ListMyTestApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1, HostCapabilities: []string{"bad"}}}, cd.ErrInvalidMyTestApplicationQuery},
		{"size", identity, ListMyTestApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}, PageSize: 101}, cd.ErrInvalidMyTestApplicationQuery},
		{"negative", identity, ListMyTestApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}, PageSize: -1}, cd.ErrInvalidMyTestApplicationQuery},
		{"token", identity, ListMyTestApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}, PageToken: strings.Repeat("x", 4097)}, cd.ErrInvalidMyTestApplicationPageToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.calls
			_, err := handler.List(t.Context(), tc.identity, tc.q)
			if !errors.Is(err, tc.want) || f.calls != before {
				t.Fatal(err)
			}
		})
	}
	if _, err := handler.Get(t.Context(), identity, GetMyTestApplicationQuery{ApplicationID: "bad", Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}}); !errors.Is(err, cd.ErrInvalidMyTestApplicationQuery) {
		t.Fatal(err)
	}
}
func TestBR_CAT_016_MyTestQueryFailClosedErrors(t *testing.T) {
	for _, want := range []error{cd.ErrInvalidMyTestApplicationPageToken, cd.ErrMyTestApplicationNotFound, cd.ErrMyTestApplicationStateInconsistent, cd.ErrMyTestApplicationUnavailable} {
		f := &myTestFake{err: want}
		_, err := NewMyTestApplications(f).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "user"}, ListMyTestApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}})
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	if !errors.Is(myTestError(errors.New("private driver details")), cd.ErrMyTestApplicationUnavailable) {
		t.Fatal("unsafe mapping")
	}
}
