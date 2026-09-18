package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestApplicationName_BR_APP_003_BoundariesAndAllowedCharacters(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "length one", value: "a", valid: true},
		{name: "length fifty", value: strings.Repeat("A", 50), valid: true},
		{name: "empty", value: "", valid: false},
		{name: "length fifty one", value: strings.Repeat("a", 51), valid: false},
		{name: "allowed characters", value: "Course_App-2026", valid: true},
		{name: "space", value: "course app", valid: false},
		{name: "dot", value: "course.app", valid: false},
		{name: "non ASCII", value: "课程", valid: false},
		{name: "leading whitespace is not trimmed", value: " course", valid: false},
		{name: "trailing whitespace is not trimmed", value: "course ", valid: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			name, err := NewApplicationName(testCase.value)
			if testCase.valid {
				if err != nil {
					t.Fatalf("NewApplicationName() error = %v", err)
				}
				if name.String() != testCase.value {
					t.Fatalf("String() = %q, want %q", name.String(), testCase.value)
				}
				return
			}

			if !errors.Is(err, ErrInvalidApplicationName) {
				t.Fatalf("NewApplicationName() error = %v, want InvalidApplicationName", err)
			}
			if errCode(t, err) != ErrorCodeInvalidApplicationName {
				t.Fatalf("error code = %q, want %q", errCode(t, err), ErrorCodeInvalidApplicationName)
			}
		})
	}
}

func TestApplicationName_BR_APP_003_PreservesValueAndBuildsASCIILowercaseKey(t *testing.T) {
	t.Parallel()

	upper, err := NewApplicationName("Course_App")
	if err != nil {
		t.Fatalf("NewApplicationName() error = %v", err)
	}
	lower, err := NewApplicationName("course_app")
	if err != nil {
		t.Fatalf("NewApplicationName() error = %v", err)
	}

	if upper.String() != "Course_App" {
		t.Fatalf("String() = %q, want original display form", upper.String())
	}
	if upper.Key() != lower.Key() || upper.Key() != "course_app" {
		t.Fatalf("comparison keys = %q and %q, want course_app", upper.Key(), lower.Key())
	}
}

func errCode(t *testing.T, err error) ErrorCode {
	t.Helper()

	var applicationError *Error
	if !errors.As(err, &applicationError) {
		t.Fatalf("error %v is not a domain Error", err)
	}
	return applicationError.Code()
}
