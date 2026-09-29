package domain

import (
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
)

const MaximumOAuthRedirectURIsPerClientType = 10

var reservedOAuthResponseParameters = map[string]struct{}{
	"code": {}, "state": {}, "iss": {}, "error": {},
	"error_description": {}, "error_uri": {},
}

// OAuthRedirectConfiguration is immutable reviewed Version content. The two
// arrays remain distinct because PUBLIC PKCE and CONFIDENTIAL clients have
// different runtime credential requirements.
type OAuthRedirectConfiguration struct {
	pkceRedirectURIs         []string
	confidentialRedirectURIs []string
}

func NewOAuthRedirectConfiguration(pkce, confidential []string) (OAuthRedirectConfiguration, error) {
	if pkce == nil || confidential == nil || len(pkce) > MaximumOAuthRedirectURIsPerClientType || len(confidential) > MaximumOAuthRedirectURIsPerClientType {
		return OAuthRedirectConfiguration{}, ErrInvalidOAuthRedirectConfiguration
	}
	pkceCopy, err := validateOAuthRedirectSet(pkce)
	if err != nil {
		return OAuthRedirectConfiguration{}, err
	}
	confidentialCopy, err := validateOAuthRedirectSet(confidential)
	if err != nil {
		return OAuthRedirectConfiguration{}, err
	}
	for _, redirectURI := range pkceCopy {
		if _, found := slices.BinarySearch(confidentialCopy, redirectURI); found {
			return OAuthRedirectConfiguration{}, ErrInvalidOAuthRedirectConfiguration
		}
	}
	return OAuthRedirectConfiguration{pkceRedirectURIs: pkceCopy, confidentialRedirectURIs: confidentialCopy}, nil
}

func EmptyOAuthRedirectConfiguration() OAuthRedirectConfiguration {
	configuration, _ := NewOAuthRedirectConfiguration([]string{}, []string{})
	return configuration
}

func (configuration OAuthRedirectConfiguration) PKCERedirectURIs() []string {
	return append([]string{}, configuration.pkceRedirectURIs...)
}

func (configuration OAuthRedirectConfiguration) ConfidentialRedirectURIs() []string {
	return append([]string{}, configuration.confidentialRedirectURIs...)
}

func (configuration OAuthRedirectConfiguration) Equal(other OAuthRedirectConfiguration) bool {
	return slices.Equal(configuration.pkceRedirectURIs, other.pkceRedirectURIs) &&
		slices.Equal(configuration.confidentialRedirectURIs, other.confidentialRedirectURIs)
}

func (configuration OAuthRedirectConfiguration) valid() bool {
	validated, err := NewOAuthRedirectConfiguration(configuration.pkceRedirectURIs, configuration.confidentialRedirectURIs)
	return err == nil && configuration.Equal(validated)
}

func validateOAuthRedirectSet(values []string) ([]string, error) {
	result := append([]string{}, values...)
	seen := make(map[string]struct{}, len(result))
	for _, value := range result {
		if _, exists := seen[value]; exists || !validOAuthRedirectURI(value) {
			return nil, ErrInvalidOAuthRedirectConfiguration
		}
		seen[value] = struct{}{}
	}
	sort.Strings(result)
	return result, nil
}

func validOAuthRedirectURI(value string) bool {
	if value == "" || len(value) > MaximumLaunchURLBytes {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(value, "#") {
		return false
	}
	host := parsed.Hostname()
	if strings.Contains(host, "*") || net.ParseIP(host) != nil {
		return false
	}
	canonical, err := canonicalOAuthHostname(host)
	if err != nil || host != canonical || isSpecialUseOAuthHostname(canonical) {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key := range query {
		if _, reserved := reservedOAuthResponseParameters[key]; reserved {
			return false
		}
	}
	return true
}

func canonicalOAuthHostname(host string) (string, error) {
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", ErrInvalidOAuthRedirectConfiguration
	}
	for _, character := range host {
		if character > 0x7f || (character >= 'A' && character <= 'Z') {
			return "", ErrInvalidOAuthRedirectConfiguration
		}
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidOAuthRedirectConfiguration
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return "", ErrInvalidOAuthRedirectConfiguration
			}
		}
	}
	if len(host) > 253 {
		return "", ErrInvalidOAuthRedirectConfiguration
	}
	return host, nil
}

func isSpecialUseOAuthHostname(host string) bool {
	for _, special := range oauthSpecialUseDomainNames {
		if host == special || strings.HasSuffix(host, "."+special) {
			return true
		}
	}
	return false
}

// Frozen IANA Special-Use Domain Names registry snapshot dated 2026-05-22,
// shared semantically with the submit-v1 launch URL preflight policy.
var oauthSpecialUseDomainNames = []string{
	"alt", "6tisch.arpa", "eap.arpa", "eap-noob.arpa", "home.arpa",
	"10.in-addr.arpa", "254.169.in-addr.arpa", "16.172.in-addr.arpa", "17.172.in-addr.arpa",
	"18.172.in-addr.arpa", "19.172.in-addr.arpa", "20.172.in-addr.arpa", "21.172.in-addr.arpa",
	"22.172.in-addr.arpa", "23.172.in-addr.arpa", "24.172.in-addr.arpa", "25.172.in-addr.arpa",
	"26.172.in-addr.arpa", "27.172.in-addr.arpa", "28.172.in-addr.arpa", "29.172.in-addr.arpa",
	"30.172.in-addr.arpa", "31.172.in-addr.arpa", "170.0.0.192.in-addr.arpa", "171.0.0.192.in-addr.arpa",
	"168.192.in-addr.arpa", "8.e.f.ip6.arpa", "9.e.f.ip6.arpa", "a.e.f.ip6.arpa", "b.e.f.ip6.arpa",
	"ipv4only.arpa", "resolver.arpa", "service.arpa", "example", "example.com", "example.net",
	"example.org", "invalid", "local", "localhost", "onion", "test",
}
