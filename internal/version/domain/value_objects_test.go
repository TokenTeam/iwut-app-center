package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestVersionLabel_BR_VER_003_CodePointWhitespaceControlAndPreservation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "one code point", value: "v", valid: true},
		{name: "fifty unicode code points", value: strings.Repeat("版", 50), valid: true},
		{name: "empty", value: "", valid: false},
		{name: "fifty one unicode code points", value: strings.Repeat("版", 51), valid: false},
		{name: "leading ascii whitespace", value: " v1", valid: false},
		{name: "trailing unicode whitespace", value: "v1\u00a0", valid: false},
		{name: "internal whitespace preserved", value: "release candidate", valid: true},
		{name: "control character", value: "v1\u0000x", valid: false},
		{name: "invalid utf8", value: string([]byte{0xff}), valid: false},
		{name: "case sensitive original", value: "V1.0.0-BETA", valid: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			label, err := NewVersionLabel(testCase.value)
			if testCase.valid {
				if err != nil || label.String() != testCase.value {
					t.Fatalf("NewVersionLabel() = (%q, %v), want preserved value", label, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidVersionLabel) {
				t.Fatalf("NewVersionLabel() error = %v, want InvalidVersionLabel", err)
			}
		})
	}
}

func TestLaunchURL_BR_VER_004_AddressClassesAndSyntax(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		value         string
		valid         bool
		developmental bool
	}{
		{name: "public HTTPS", value: "https://example.edu/app?q=1#top", valid: true},
		{name: "HTTPS localhost remains syntactically valid draft", value: "https://localhost/app", valid: true},
		{name: "localhost", value: "http://localhost:8081/", valid: true, developmental: true},
		{name: "localhost subdomain", value: "http://app.localhost/", valid: true, developmental: true},
		{name: "mDNS local", value: "http://expo.local/", valid: true, developmental: true},
		{name: "IPv4 loopback", value: "http://127.0.0.1:3000/", valid: true, developmental: true},
		{name: "RFC1918 10", value: "http://10.0.0.1/", valid: true, developmental: true},
		{name: "RFC1918 172", value: "http://172.16.255.1/", valid: true, developmental: true},
		{name: "RFC1918 192", value: "http://192.168.1.1/", valid: true, developmental: true},
		{name: "IPv6 loopback", value: "http://[::1]:8081/", valid: true, developmental: true},
		{name: "IPv6 ULA", value: "http://[fd12:3456::1]/", valid: true, developmental: true},
		{name: "IPv6 link local", value: "http://[fe80::1]/", valid: true, developmental: true},
		{name: "public hostname HTTP", value: "http://example.edu/", valid: false},
		{name: "public IPv4 HTTP", value: "http://8.8.8.8/", valid: false},
		{name: "outside RFC1918", value: "http://172.15.0.1/", valid: false},
		{name: "IPv4 link local not in allowed list", value: "http://169.254.1.1/", valid: false},
		{name: "userinfo", value: "https://user:pass@example.edu/", valid: false},
		{name: "relative", value: "/app", valid: false},
		{name: "missing host", value: "https:///app", valid: false},
		{name: "file", value: "file:///tmp/app", valid: false},
		{name: "data", value: "data:text/html,hello", valid: false},
		{name: "javascript", value: "javascript:alert(1)", valid: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			launchURL, err := NewLaunchURL(testCase.value)
			if testCase.valid {
				if err != nil || launchURL.String() != testCase.value || launchURL.IsDevelopmental() != testCase.developmental {
					t.Fatalf("NewLaunchURL() = (%#v, %v), want valid developmental=%v", launchURL, err, testCase.developmental)
				}
				return
			}
			if !errors.Is(err, ErrInvalidApplicationLaunchURL) {
				t.Fatalf("NewLaunchURL() error = %v, want InvalidApplicationLaunchUrl", err)
			}
		})
	}
}

func TestLaunchURL_BR_VER_004_UTF8ByteBoundary(t *testing.T) {
	t.Parallel()

	prefix := "https://example.edu/"
	atLimit := prefix + strings.Repeat("a", MaximumLaunchURLBytes-len(prefix))
	overLimit := atLimit + "a"
	if len(atLimit) != 2048 || len(overLimit) != 2049 {
		t.Fatal("test setup has incorrect byte lengths")
	}
	if _, err := NewLaunchURL(atLimit); err != nil {
		t.Fatalf("2048-byte URL rejected: %v", err)
	}
	if _, err := NewLaunchURL(overLimit); !errors.Is(err, ErrInvalidApplicationLaunchURL) {
		t.Fatalf("2049-byte URL error = %v, want InvalidApplicationLaunchUrl", err)
	}

	unicodeAtLimit := prefix + strings.Repeat("界", (MaximumLaunchURLBytes-len(prefix))/len("界"))
	unicodeAtLimit += strings.Repeat("a", MaximumLaunchURLBytes-len(unicodeAtLimit))
	if len(unicodeAtLimit) != 2048 {
		t.Fatal("unicode test setup has incorrect byte length")
	}
	if _, err := NewLaunchURL(unicodeAtLimit); err != nil {
		t.Fatalf("2048-byte Unicode URL rejected: %v", err)
	}
	if _, err := NewLaunchURL(unicodeAtLimit + "界"); !errors.Is(err, ErrInvalidApplicationLaunchURL) {
		t.Fatalf("over-limit Unicode URL error = %v, want InvalidApplicationLaunchUrl", err)
	}
}

func TestRPCApiRange_BR_VER_005_Boundaries(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		minimum int32
		maximum int32
		valid   bool
	}{
		{name: "single major", minimum: 1, maximum: 2, valid: true},
		{name: "multiple majors", minimum: 3, maximum: 7, valid: true},
		{name: "minimum zero", minimum: 0, maximum: 1},
		{name: "equal upper bound", minimum: 2, maximum: 2},
		{name: "lower upper bound", minimum: 3, maximum: 2},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			apiRange, err := NewRPCApiRange(testCase.minimum, testCase.maximum)
			if testCase.valid {
				if err != nil || apiRange.Minimum() != testCase.minimum || apiRange.MaximumExclusive() != testCase.maximum {
					t.Fatalf("NewRPCApiRange() = (%#v, %v), want valid", apiRange, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidRPCApiRange) {
				t.Fatalf("NewRPCApiRange() error = %v, want InvalidRpcApiRange", err)
			}
		})
	}
}

func TestCapabilitySet_BR_VER_006_FormatDuplicateSortAndEmpty(t *testing.T) {
	t.Parallel()

	set, err := NewCapabilitySet([]string{"user.profile.v2", "camera.read.v1", "camera.flash.v10"})
	if err != nil {
		t.Fatalf("NewCapabilitySet() error = %v", err)
	}
	if want := []string{"camera.flash.v10", "camera.read.v1", "user.profile.v2"}; !reflect.DeepEqual(set.Strings(), want) {
		t.Fatalf("Strings() = %v, want %v", set.Strings(), want)
	}

	empty, err := NewCapabilitySet(nil)
	if err != nil || empty.Strings() == nil || len(empty.Strings()) != 0 {
		t.Fatalf("empty set = (%v, %v), want non-nil empty slice", empty.Strings(), err)
	}

	invalid := [][]string{
		{"camera.read.v1", "camera.read.v1"},
		{"Camera.read.v1"},
		{"camera-read.v1"},
		{"camera..read.v1"},
		{"camera.read"},
		{"camera.read.v0"},
		{"camera.read.v01"},
		{"camera.read.v-1"},
		{"1camera.read.v1"},
	}
	for _, values := range invalid {
		if _, err := NewCapabilitySet(values); !errors.Is(err, ErrInvalidRequiredCapability) {
			t.Errorf("NewCapabilitySet(%v) error = %v, want InvalidRequiredCapability", values, err)
		}
	}
}

func TestScopeRequest_BR_VER_007_OpaqueDuplicateCrossSortAndEmpty(t *testing.T) {
	t.Parallel()

	request, err := NewScopeRequest(
		[]string{"用户.资料", "", "profile.basic"},
		[]string{"schedule/read", "Email.Read"},
	)
	if err != nil {
		t.Fatalf("NewScopeRequest() error = %v", err)
	}
	if want := []ScopeName{"", "profile.basic", "用户.资料"}; !reflect.DeepEqual(request.Required(), want) {
		t.Fatalf("Required() = %v, want %v", request.Required(), want)
	}
	if want := []ScopeName{"Email.Read", "schedule/read"}; !reflect.DeepEqual(request.Optional(), want) {
		t.Fatalf("Optional() = %v, want %v", request.Optional(), want)
	}
	if want := []ScopeName{"", "Email.Read", "profile.basic", "schedule/read", "用户.资料"}; !reflect.DeepEqual(request.All(), want) {
		t.Fatalf("All() = %v, want %v", request.All(), want)
	}

	empty, err := NewScopeRequest(nil, nil)
	if err != nil || empty.Required() == nil || empty.Optional() == nil {
		t.Fatalf("empty request = (%v, %v, %v), want non-nil empty slices", empty.Required(), empty.Optional(), err)
	}

	invalid := []struct {
		required []string
		optional []string
	}{
		{required: []string{"profile", "profile"}},
		{optional: []string{"profile", "profile"}},
		{required: []string{"profile"}, optional: []string{"profile"}},
	}
	for _, values := range invalid {
		if _, err := NewScopeRequest(values.required, values.optional); !errors.Is(err, ErrInvalidApplicationScope) {
			t.Errorf("NewScopeRequest(%v, %v) error = %v, want InvalidApplicationScope", values.required, values.optional, err)
		}
	}
}
