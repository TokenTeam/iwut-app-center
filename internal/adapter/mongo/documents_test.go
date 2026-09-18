package mongo

import (
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
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
