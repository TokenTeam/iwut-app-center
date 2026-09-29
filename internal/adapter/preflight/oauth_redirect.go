package preflight

import (
	"net/url"
	"strings"

	"golang.org/x/net/idna"
	reviewport "iwut-app-center/internal/review/port"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

type OAuthRedirectPolicy struct{}

func NewOAuthRedirectPolicy() *OAuthRedirectPolicy { return &OAuthRedirectPolicy{} }

func (*OAuthRedirectPolicy) EnsureCanonical(pkceRedirectURIs, confidentialRedirectURIs []string) error {
	for _, value := range append(append([]string{}, pkceRedirectURIs...), confidentialRedirectURIs...) {
		parsed, err := url.Parse(value)
		if err != nil {
			return versionport.ErrInvalidOAuthRedirectConfiguration
		}
		host := parsed.Hostname()
		alabel, err := idna.Lookup.ToASCII(host)
		if err != nil || strings.ToLower(alabel) != host {
			return versionport.ErrInvalidOAuthRedirectConfiguration
		}
	}
	return nil
}

func (policy *OAuthRedirectPolicy) Validate(pkceRedirectURIs, confidentialRedirectURIs []string) error {
	if _, err := versiondomain.NewOAuthRedirectConfiguration(pkceRedirectURIs, confidentialRedirectURIs); err != nil {
		return reviewport.ErrOAuthRedirectNotReviewable
	}
	if err := policy.EnsureCanonical(pkceRedirectURIs, confidentialRedirectURIs); err != nil {
		return reviewport.ErrOAuthRedirectNotReviewable
	}
	return nil
}

var _ versionport.OAuthRedirectPolicy = (*OAuthRedirectPolicy)(nil)
var _ reviewport.OAuthRedirectPolicy = (*OAuthRedirectPolicy)(nil)
