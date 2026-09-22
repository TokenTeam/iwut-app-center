package testercredential

import (
	"encoding/base64"
	"errors"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
	"net/url"
	"strings"
	"unicode"
)

type TesterJoinURLBuilder struct{ prefix string }

func NewTesterJoinURLBuilder(prefix string) (*TesterJoinURLBuilder, error) {
	if !validPrefix(prefix) {
		return nil, errors.New("invalid tester join URL prefix")
	}
	return &TesterJoinURLBuilder{prefix}, nil
}
func validPrefix(prefix string) bool {
	if prefix == "" || strings.TrimSpace(prefix) != prefix || strings.ContainsAny(prefix, "?#") || strings.ContainsFunc(prefix, unicode.IsSpace) {
		return false
	}
	parsed, err := url.Parse(prefix)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == "" && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == ""
}
func (b *TesterJoinURLBuilder) Build(id testerdomain.ApplicationTesterJoinLinkID, rawToken string) (string, error) {
	if b == nil || !validPrefix(b.prefix) || !id.IsValid() {
		return "", errors.New("invalid tester join URL input")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(rawToken)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != rawToken {
		return "", errors.New("invalid tester join URL credential")
	}
	fragment := url.Values{"joinLinkId": {strings.ToLower(id.String())}, "secret": {rawToken}}.Encode()
	return b.prefix + "#" + fragment, nil
}

var _ testerport.TesterJoinURLBuilder = (*TesterJoinURLBuilder)(nil)
