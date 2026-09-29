package preflight

import (
	"errors"
	"testing"

	reviewport "iwut-app-center/internal/review/port"
	versionport "iwut-app-center/internal/version/port"
)

func TestOAuthRedirectPolicy_RequiresCanonicalUTS46ALabel(t *testing.T) {
	t.Parallel()
	policy := NewOAuthRedirectPolicy()
	tests := []struct {
		name string
		uri  string
		want error
	}{
		{name: "ordinary ASCII hostname", uri: "https://app.example.edu/oauth/callback"},
		{name: "canonical A-label", uri: "https://xn--bcher-kva.example.edu/oauth/callback"},
		{name: "Unicode U-label", uri: "https://bücher.example.edu/oauth/callback", want: versionport.ErrInvalidOAuthRedirectConfiguration},
		{name: "malformed A-label", uri: "https://xn--a.example.edu/oauth/callback", want: versionport.ErrInvalidOAuthRedirectConfiguration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := policy.EnsureCanonical([]string{test.uri}, []string{})
			if !errors.Is(err, test.want) {
				t.Fatalf("EnsureCanonical() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOAuthRedirectPolicy_RevalidatesCompleteReviewedConfiguration(t *testing.T) {
	t.Parallel()
	policy := NewOAuthRedirectPolicy()
	if err := policy.Validate([]string{"https://app.example.edu/oauth/callback"}, []string{}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, invalid := range []string{
		"http://app.example.edu/oauth/callback",
		"https://localhost/oauth/callback",
		"https://bücher.example.edu/oauth/callback",
	} {
		if err := policy.Validate([]string{invalid}, []string{}); !errors.Is(err, reviewport.ErrOAuthRedirectNotReviewable) {
			t.Fatalf("Validate(%q) error = %v, want not reviewable", invalid, err)
		}
	}
}
