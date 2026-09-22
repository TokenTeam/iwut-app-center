package testercredential

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	domain "iwut-app-center/internal/tester/domain"
	"net/url"
	"strings"
	"testing"
)

const id domain.ApplicationTesterJoinLinkID = "01900000-0000-7000-8000-00000000000a"

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("sensitive entropy source details")
}
func TestBRTST004RealEntropyAndRawByteHash(t *testing.T) {
	f := NewSecureTesterJoinTokenFactory()
	one, hash, err := f.NewToken()
	if err != nil {
		t.Fatal("token factory failed")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(one)
	if err != nil || len(decoded) != 32 || strings.Contains(one, "=") || sha256.Sum256(decoded) != hash {
		t.Fatal("token/hash contract violated")
	}
	two, _, err := f.NewToken()
	if err != nil || two == one {
		t.Fatal("factory did not produce distinct random credentials")
	}
	// This deterministic vector proves hashing uses the raw bytes, not base64 text.
	raw := bytes.Repeat([]byte{0x6a}, 32)
	f = &SecureTesterJoinTokenFactory{reader: bytes.NewReader(raw)}
	token, got, err := f.NewToken()
	if err != nil || token != base64.RawURLEncoding.EncodeToString(raw) || got != sha256.Sum256(raw) || got == sha256.Sum256([]byte(token)) {
		t.Fatal("wrong SHA-256 input")
	}
}
func TestBRTST004EntropyFailureNeverReturnsPartialCredential(t *testing.T) {
	for _, factory := range []*SecureTesterJoinTokenFactory{{reader: failingReader{}}, {reader: bytes.NewReader(make([]byte, 31))}, {}, nil} {
		raw, hash, err := factory.NewToken()
		if raw != "" || hash != [32]byte{} || err == nil || strings.Contains(err.Error(), "sensitive") {
			t.Fatal("partial credential or sensitive cause escaped")
		}
	}
}
func TestBRTST004008JoinURLFragmentContract(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{255}, 32))
	for _, prefix := range []string{"https://app.example/tester/join", "http://localhost:8080/dev/join/", "https://example.invalid/中文/%2F/path/", "http://[::1]:8080/"} {
		t.Run(prefix, func(t *testing.T) {
			builder, err := NewTesterJoinURLBuilder(prefix)
			if err != nil {
				t.Fatal(err)
			}
			value, err := builder.Build(domain.ApplicationTesterJoinLinkID(strings.ToUpper(id.String())), secret)
			if err != nil {
				t.Fatal(err)
			}
			if value != prefix+"#joinLinkId="+id.String()+"&secret="+secret {
				t.Fatal("prefix/fragment changed")
			}
			parsed, err := url.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			query, err := url.ParseQuery(parsed.Fragment)
			if err != nil || len(query) != 2 || len(query["joinLinkId"]) != 1 || query.Get("joinLinkId") != id.String() || query.Get("secret") != secret || parsed.RawQuery != "" || strings.Contains(parsed.Path, secret) {
				t.Fatal("fragment does not roundtrip")
			}
		})
	}
}
func TestBRTST004008InvalidPrefixOrCredentialFailsClosed(t *testing.T) {
	for _, prefix := range []string{"", " ", " https://app.example/join", "https://app.example/join ", "/join", "ftp://host/join", "https:///join", "https://", "https://user@host/join", "https://host/join?", "https://host/join?a=b", "https://host/join#", "https://host/join#x", "https://host/%zz", "https://host:bad/join", "https://host/join\n", "https://host/path with spaces"} {
		if builder, err := NewTesterJoinURLBuilder(prefix); err == nil || builder != nil {
			t.Fatalf("accepted invalid prefix %q", prefix)
		}
	}
	builder, _ := NewTesterJoinURLBuilder("https://app.example/join")
	canonical := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, token := range []string{"", canonical + "=", canonical[:20] + "\r\n" + canonical[20:], canonical[:42] + "B", base64.RawURLEncoding.EncodeToString(make([]byte, 31)), base64.RawURLEncoding.EncodeToString(make([]byte, 33)), strings.Repeat("+", 43)} {
		value, err := builder.Build(id, token)
		if err == nil || value != "" || strings.Contains(err.Error(), token) && token != "" {
			t.Fatal("invalid credential returned or echoed")
		}
	}
	if value, err := builder.Build("not-an-id", canonical); err == nil || value != "" {
		t.Fatal("invalid ID returned")
	}
}
func TestBRTST004HashDiagnosticRedaction(t *testing.T) {
	hash := domain.NewTesterJoinTokenHash(sha256.Sum256([]byte("fixture")))
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(format, hash); !strings.Contains(got, "redacted") {
			t.Fatal("hash diagnostic not redacted")
		}
	}
}
