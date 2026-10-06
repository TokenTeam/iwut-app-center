package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const validApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"

func TestApplicationID_BR_APP_001_RequiresUUIDv7(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "UUIDv7", value: validApplicationID, valid: true},
		{name: "uppercase UUIDv7", value: "01890F5A-E810-7CC3-98C8-8C6D5D8B4C21", valid: true},
		{name: "UUIDv4", value: "550e8400-e29b-41d4-a716-446655440000", valid: false},
		{name: "invalid variant", value: "01890f5a-e810-7cc3-78c8-8c6d5d8b4c21", valid: false},
		{name: "malformed", value: "not-a-uuid", valid: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			id, err := ParseApplicationID(testCase.value)
			if testCase.valid {
				if err != nil {
					t.Fatalf("ParseApplicationID() error = %v", err)
				}
				if !id.IsValid() {
					t.Fatal("parsed ID is not valid")
				}
				return
			}
			if !errors.Is(err, ErrInvalidApplicationID) {
				t.Fatalf("ParseApplicationID() error = %v, want InvalidApplicationId", err)
			}
			if errCode(t, err) != ErrorCodeInvalidApplicationID {
				t.Fatalf("error code = %q, want %q", errCode(t, err), ErrorCodeInvalidApplicationID)
			}
			var applicationError *Error
			if !errors.As(err, &applicationError) || applicationError.Category() != ErrorCategoryValidation {
				t.Fatalf("error category = %v, want Validation", applicationError)
			}
		})
	}
}

func TestApplication_BR_APP_001_InvalidIdentityIsInternalInvariantFailure(t *testing.T) {
	t.Parallel()

	name, err := NewApplicationName("app")
	if err != nil {
		t.Fatalf("NewApplicationName() error = %v", err)
	}
	adminID, err := NewAuthID("auth-1")
	if err != nil {
		t.Fatalf("NewAuthID() error = %v", err)
	}

	application, err := NewApplication(
		ApplicationID("corrupt-persisted-id"),
		name,
		adminID,
		time.Now(),
	)
	if application != nil || !errors.Is(err, ErrInternal) {
		t.Fatalf("NewApplication() = (%v, %v), want nil and Internal", application, err)
	}
}

func TestApplication_BR_APP_001_002_007_FieldsAreSetAtCreation(t *testing.T) {
	t.Parallel()

	id, err := ParseApplicationID(validApplicationID)
	if err != nil {
		t.Fatalf("ParseApplicationID() error = %v", err)
	}
	name, err := NewApplicationName("Course_App")
	if err != nil {
		t.Fatalf("NewApplicationName() error = %v", err)
	}
	adminID, err := NewAuthID("auth-42")
	if err != nil {
		t.Fatalf("NewAuthID() error = %v", err)
	}
	createdAt := time.Date(2026, time.September, 19, 12, 30, 0, 123, time.FixedZone("CST", 8*60*60))

	application, err := NewApplication(id, name, adminID, createdAt)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	if application.ID() != id {
		t.Fatalf("ID() = %q, want %q", application.ID(), id)
	}
	if application.Name() != name {
		t.Fatalf("Name() = %#v, want %#v", application.Name(), name)
	}
	if application.AdminID() != adminID {
		t.Fatalf("AdminID() = %q, want %q", application.AdminID(), adminID)
	}
	if !application.CreatedAt().Equal(createdAt) {
		t.Fatalf("CreatedAt() = %v, want instant %v", application.CreatedAt(), createdAt)
	}
	if application.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt() location = %v, want UTC", application.CreatedAt().Location())
	}
}

func TestApplication_BR_APP_020_ContainsLifecycleFields(t *testing.T) {
	t.Parallel()

	applicationType := reflect.TypeOf(Application{})
	wantFields := []string{"id", "name", "adminID", "lifecycleStatus", "lifecycleRevision", "createdAt"}
	if applicationType.NumField() != len(wantFields) {
		t.Fatalf("Application has %d fields, want %d", applicationType.NumField(), len(wantFields))
	}
	for index, want := range wantFields {
		if got := applicationType.Field(index).Name; got != want {
			t.Fatalf("Application field %d = %q, want %q", index, got, want)
		}
	}
}
