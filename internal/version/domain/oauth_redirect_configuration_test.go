package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestOAuthRedirectConfiguration_BR_VER_018_NormalizesTypedSets(t *testing.T) {
	t.Parallel()
	configuration, err := NewOAuthRedirectConfiguration(
		[]string{"https://b.example.edu/callback", "https://a.example.edu/oauth/callback?tenant=1"},
		[]string{"https://server.example.edu/callback"},
	)
	if err != nil {
		t.Fatalf("NewOAuthRedirectConfiguration() error = %v", err)
	}
	if got, want := configuration.PKCERedirectURIs(), []string{"https://a.example.edu/oauth/callback?tenant=1", "https://b.example.edu/callback"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PKCE redirects = %v, want %v", got, want)
	}
	if got := configuration.ConfidentialRedirectURIs(); !reflect.DeepEqual(got, []string{"https://server.example.edu/callback"}) {
		t.Fatalf("confidential redirects = %v", got)
	}
	configuration.PKCERedirectURIs()[0] = "changed"
	if configuration.PKCERedirectURIs()[0] == "changed" {
		t.Fatal("redirect getter exposed mutable domain state")
	}
}

func TestOAuthRedirectConfiguration_BR_VER_018_RejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	valid := "https://app.example.edu/oauth/callback"
	tests := []struct {
		name         string
		pkce         []string
		confidential []string
	}{
		{name: "nil PKCE array", pkce: nil, confidential: []string{}},
		{name: "nil confidential array", pkce: []string{}, confidential: nil},
		{name: "duplicate", pkce: []string{valid, valid}, confidential: []string{}},
		{name: "cross type overlap", pkce: []string{valid}, confidential: []string{valid}},
		{name: "HTTP", pkce: []string{"http://app.example.edu/callback"}, confidential: []string{}},
		{name: "userinfo", pkce: []string{"https://user@app.example.edu/callback"}, confidential: []string{}},
		{name: "fragment", pkce: []string{"https://app.example.edu/callback#result"}, confidential: []string{}},
		{name: "wildcard", pkce: []string{"https://*.example.edu/callback"}, confidential: []string{}},
		{name: "IP literal", pkce: []string{"https://192.0.2.1/callback"}, confidential: []string{}},
		{name: "localhost", pkce: []string{"https://localhost/callback"}, confidential: []string{}},
		{name: "special use child", pkce: []string{"https://app.example.com/callback"}, confidential: []string{}},
		{name: "noncanonical case", pkce: []string{"https://APP.example.edu/callback"}, confidential: []string{}},
		{name: "noncanonical Unicode", pkce: []string{"https://bücher.example.edu/callback"}, confidential: []string{}},
		{name: "reserved query", pkce: []string{"https://app.example.edu/callback?state=owned"}, confidential: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewOAuthRedirectConfiguration(test.pkce, test.confidential); !errors.Is(err, ErrInvalidOAuthRedirectConfiguration) {
				t.Fatalf("NewOAuthRedirectConfiguration() error = %v, want InvalidOAuthRedirectConfiguration", err)
			}
		})
	}
}
