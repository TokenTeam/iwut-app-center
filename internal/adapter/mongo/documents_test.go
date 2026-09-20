package mongo

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
)

func TestApplicationDocumentMapper_BRAPP001_BRAPP003_BRAPP007(t *testing.T) {
	id, err := domain.ParseApplicationID("01890f47-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatalf("parse ID: %v", err)
	}
	name, err := domain.NewApplicationName("Course_App")
	if err != nil {
		t.Fatalf("create name: %v", err)
	}
	adminID, err := domain.NewAuthID("auth-1")
	if err != nil {
		t.Fatalf("create admin ID: %v", err)
	}
	createdAt := time.Date(2026, time.September, 19, 8, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	application, err := domain.NewApplication(id, name, adminID, createdAt)
	if err != nil {
		t.Fatalf("create application: %v", err)
	}

	document, err := applicationToDocument(application)
	if err != nil {
		t.Fatalf("map to document: %v", err)
	}
	if document.NameKey != "course_app" {
		t.Fatalf("nameKey = %q, want course_app", document.NameKey)
	}
	if document.NextVersionSequence != 1 || document.NextProfileRevisionSequence != 1 {
		t.Fatalf("next sequences = (%d, %d), want (1, 1)", document.NextVersionSequence, document.NextProfileRevisionSequence)
	}
	if document.CreatedAt.Location() != time.UTC {
		t.Fatalf("createdAt location = %s, want UTC", document.CreatedAt.Location())
	}

	restored, err := applicationFromDocument(document)
	if err != nil {
		t.Fatalf("restore document: %v", err)
	}
	if restored.ID() != id || restored.Name().String() != "Course_App" || restored.AdminID() != adminID {
		t.Fatalf("restored application does not match source")
	}
}

func TestApplicationVersionDocumentMapper_BRVER001_BRVER006_BRVER007_BRVER009(t *testing.T) {
	draft := mapperDraftApplicationVersion(t)
	sequence, err := versiondomain.NewVersionSequence(7)
	if err != nil {
		t.Fatalf("create sequence: %v", err)
	}

	document, err := applicationVersionToDocument(draft, sequence)
	if err != nil {
		t.Fatalf("map application version to document: %v", err)
	}
	if document.Sequence != 7 || document.ReviewStatus != "DRAFT" || document.Revision != 1 {
		t.Fatalf("sequence/status/revision = %d/%q/%d, want 7/DRAFT/1", document.Sequence, document.ReviewStatus, document.Revision)
	}
	if !reflect.DeepEqual(document.RequiredCapabilities, []string{"camera.read.v1", "user.profile.v2"}) ||
		!reflect.DeepEqual(document.RequiredScopes, []string{"profile.basic", "schedule.read"}) ||
		!reflect.DeepEqual(document.OptionalScopes, []string{"email.read"}) {
		t.Fatalf("stored sets are not canonical: capabilities=%v required=%v optional=%v", document.RequiredCapabilities, document.RequiredScopes, document.OptionalScopes)
	}
	if document.RequiredCapabilities == nil || document.RequiredScopes == nil || document.OptionalScopes == nil {
		t.Fatal("stored set fields must be non-nil arrays")
	}
	if document.CreatedBy != document.UpdatedBy || !document.CreatedAt.Equal(document.UpdatedAt) {
		t.Fatal("creation and update audit fields differ")
	}

	restored, err := applicationVersionFromDocument(document)
	if err != nil {
		t.Fatalf("restore application version document: %v", err)
	}
	if restored.ID() != draft.ID() || restored.ApplicationID() != draft.ApplicationID() || restored.Sequence() != sequence ||
		restored.VersionLabel() != draft.VersionLabel() || restored.CreatedBy() != draft.CreatedBy() {
		t.Fatalf("restored version does not match source: %#v", restored)
	}
}

func TestApplicationVersionDocumentMapper_StoredValidationFailureIsCorruption(t *testing.T) {
	draft := mapperDraftApplicationVersion(t)
	sequence, err := versiondomain.NewVersionSequence(1)
	if err != nil {
		t.Fatalf("create sequence: %v", err)
	}
	valid, err := applicationVersionToDocument(draft, sequence)
	if err != nil {
		t.Fatalf("map valid document: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*applicationVersionDocument)
	}{
		{name: "invalid version ID", mutate: func(document *applicationVersionDocument) { document.VersionID = "not-a-uuid" }},
		{name: "invalid application ID", mutate: func(document *applicationVersionDocument) { document.ApplicationID = "not-a-uuid" }},
		{name: "invalid sequence", mutate: func(document *applicationVersionDocument) { document.Sequence = 0 }},
		{name: "invalid version label", mutate: func(document *applicationVersionDocument) { document.VersionLabel = " bad" }},
		{name: "invalid launch URL", mutate: func(document *applicationVersionDocument) { document.LaunchURL = "file:///tmp/app" }},
		{name: "invalid RPC range", mutate: func(document *applicationVersionDocument) {
			document.RPCApiMaxVersionExclusive = document.RPCApiMinVersion
		}},
		{name: "null capability array", mutate: func(document *applicationVersionDocument) { document.RequiredCapabilities = nil }},
		{name: "null required scope array", mutate: func(document *applicationVersionDocument) { document.RequiredScopes = nil }},
		{name: "null optional scope array", mutate: func(document *applicationVersionDocument) { document.OptionalScopes = nil }},
		{name: "duplicate capability", mutate: func(document *applicationVersionDocument) {
			document.RequiredCapabilities = []string{"camera.read.v1", "camera.read.v1"}
		}},
		{name: "unsorted capabilities", mutate: func(document *applicationVersionDocument) {
			document.RequiredCapabilities = []string{"user.profile.v2", "camera.read.v1"}
		}},
		{name: "crossed scope", mutate: func(document *applicationVersionDocument) { document.OptionalScopes = []string{"profile.basic"} }},
		{name: "unsorted scope", mutate: func(document *applicationVersionDocument) {
			document.RequiredScopes = []string{"schedule.read", "profile.basic"}
		}},
		{name: "empty created by", mutate: func(document *applicationVersionDocument) { document.CreatedBy = "" }},
		{name: "unknown status", mutate: func(document *applicationVersionDocument) { document.ReviewStatus = "UNKNOWN" }},
		{name: "invalid revision", mutate: func(document *applicationVersionDocument) { document.Revision = 0 }},
		{name: "empty updated by", mutate: func(document *applicationVersionDocument) { document.UpdatedBy = "" }},
		{name: "zero updated at", mutate: func(document *applicationVersionDocument) { document.UpdatedAt = time.Time{} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := valid
			document.RequiredCapabilities = append([]string(nil), valid.RequiredCapabilities...)
			document.RequiredScopes = append([]string(nil), valid.RequiredScopes...)
			document.OptionalScopes = append([]string(nil), valid.OptionalScopes...)
			test.mutate(&document)
			_, err := applicationVersionFromDocument(document)
			if !errors.Is(err, errCorruptApplicationVersionDocument) {
				t.Fatalf("error = %v, want stored application-version corruption", err)
			}
			if errors.Is(err, versiondomain.ErrInvalidVersionLabel) ||
				errors.Is(err, versiondomain.ErrInvalidApplicationLaunchURL) ||
				errors.Is(err, versiondomain.ErrInvalidRPCApiRange) ||
				errors.Is(err, versiondomain.ErrInvalidRequiredCapability) ||
				errors.Is(err, versiondomain.ErrInvalidApplicationScope) {
				t.Fatalf("storage corruption leaked caller validation: %v", err)
			}
		})
	}
}

func TestApplicationVersionDocumentMapper_BRVER012_BRVER013_RestoresUpdatedAuditAndLifecycle(t *testing.T) {
	draft := mapperDraftApplicationVersion(t)
	created, err := versiondomain.NewApplicationVersion(draft, 7)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	capabilities, _ := versiondomain.NewCapabilitySet([]string{"camera.read.v1", "user.profile.v2"})
	scopes, _ := versiondomain.NewScopeRequest([]string{"profile.basic"}, nil)
	label, _ := versiondomain.NewVersionLabel("v2")
	launchURL, _ := versiondomain.NewLaunchURL("https://example.edu/v2")
	rpcRange, _ := versiondomain.NewRPCApiRange(2, 5)
	replacement, _ := versiondomain.NewDraftApplicationVersionReplacement(label, launchURL, rpcRange, capabilities, scopes)
	updatedAt := time.Date(2026, time.September, 20, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	updated, err := created.ReplaceDraft(1, replacement, "auth-editor", updatedAt)
	if err != nil {
		t.Fatalf("replace draft: %v", err)
	}
	document, err := applicationVersionEntityToDocument(updated)
	if err != nil {
		t.Fatalf("map updated version: %v", err)
	}
	restored, err := applicationVersionFromDocument(document)
	if err != nil {
		t.Fatalf("restore updated version: %v", err)
	}
	if restored.Revision() != 2 || restored.UpdatedBy() != "auth-editor" || !restored.UpdatedAt().Equal(updatedAt) ||
		restored.CreatedBy() != created.CreatedBy() || !restored.CreatedAt().Equal(created.CreatedAt()) {
		t.Fatalf("restored audit is incorrect: %#v", restored)
	}
}

func mapperDraftApplicationVersion(t *testing.T) *versiondomain.DraftApplicationVersion {
	t.Helper()
	applicationID, ok := shared.ParseApplicationID("01890f47-0000-7000-8000-000000000101")
	if !ok {
		t.Fatal("parse application ID")
	}
	label, err := versiondomain.NewVersionLabel("v1.0.0")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	launchURL, err := versiondomain.NewLaunchURL("https://example.edu/app")
	if err != nil {
		t.Fatalf("create launch URL: %v", err)
	}
	rpcRange, err := versiondomain.NewRPCApiRange(2, 4)
	if err != nil {
		t.Fatalf("create RPC range: %v", err)
	}
	capabilities, err := versiondomain.NewCapabilitySet([]string{"user.profile.v2", "camera.read.v1"})
	if err != nil {
		t.Fatalf("create capabilities: %v", err)
	}
	scopes, err := versiondomain.NewScopeRequest([]string{"schedule.read", "profile.basic"}, []string{"email.read"})
	if err != nil {
		t.Fatalf("create scopes: %v", err)
	}
	draft, err := versiondomain.NewDraftApplicationVersion(
		versiondomain.ApplicationVersionID("01890f47-0000-7000-8000-000000000201"),
		applicationID,
		label,
		launchURL,
		rpcRange,
		capabilities,
		scopes,
		shared.AuthID("auth-version-mapper"),
		time.Date(2026, time.September, 20, 1, 2, 3, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return draft
}

func TestApplicationDocumentMapper_StoredValidationFailureIsCorruption(t *testing.T) {
	valid := applicationDocument{
		ID:                          "01890f47-0000-7000-8000-000000000001",
		Name:                        "Course_App",
		NameKey:                     "course_app",
		AdminID:                     "auth-1",
		CreatedAt:                   time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC),
		NextVersionSequence:         1,
		NextProfileRevisionSequence: 1,
	}

	tests := []struct {
		name   string
		mutate func(*applicationDocument)
	}{
		{name: "invalid UUID version", mutate: func(document *applicationDocument) { document.ID = "01890f47-0000-4000-8000-000000000001" }},
		{name: "name comparison key mismatch", mutate: func(document *applicationDocument) { document.NameKey = "another" }},
		{name: "empty admin", mutate: func(document *applicationDocument) { document.AdminID = "" }},
		{name: "zero audit time", mutate: func(document *applicationDocument) { document.CreatedAt = time.Time{} }},
		{name: "invalid version sequence", mutate: func(document *applicationDocument) { document.NextVersionSequence = 0 }},
		{name: "invalid profile sequence", mutate: func(document *applicationDocument) { document.NextProfileRevisionSequence = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := valid
			test.mutate(&document)
			_, err := applicationFromDocument(document)
			if !errors.Is(err, errCorruptApplicationDocument) {
				t.Fatalf("error = %v, want stored document corruption", err)
			}
			if errors.Is(err, domain.ErrInvalidApplicationID) || errors.Is(err, domain.ErrInvalidApplicationName) {
				t.Fatalf("storage corruption leaked caller validation: %v", err)
			}
		})
	}
}
