package preflight

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
)

type deterministicResolver struct {
	addresses []netip.Addr
	err       error
	calls     int
	host      string
}

func (resolver *deterministicResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	resolver.calls++
	resolver.host = host
	if network != "ip" {
		return nil, errors.New("unexpected network")
	}
	return append([]netip.Addr{}, resolver.addresses...), resolver.err
}

func TestLaunchURLSubmissionPolicy_BR_REV_007(t *testing.T) {
	publicV4 := netip.MustParseAddr("8.8.8.8")
	publicV6 := netip.MustParseAddr("2606:4700:4700::1111")
	tests := []struct {
		name      string
		url       string
		addresses []netip.Addr
		resolver  error
		want      error
		calls     int
		wantHost  string
	}{
		{name: "public dual stack", url: "HTTPS://app.example.edu/path", addresses: []netip.Addr{publicV4, publicV6}, calls: 1, wantHost: "app.example.edu"},
		{name: "one DNS root dot", url: "https://app.example.edu./path", addresses: []netip.Addr{publicV4}, calls: 1, wantHost: "app.example.edu"},
		{name: "IDNA Lookup A-label", url: "https://bücher.example.edu/path", addresses: []netip.Addr{publicV4}, calls: 1, wantHost: "xn--bcher-kva.example.edu"},
		{name: "two root dots", url: "https://app.example.edu../path", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "HTTP", url: "http://app.example.edu", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "userinfo", url: "https://user@app.example.edu", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "localhost", url: "https://localhost/app", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "localhost suffix", url: "https://api.localhost/app", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "mDNS local", url: "https://printer.local/app", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "literal public IPv4 still bypasses DNS", url: "https://8.8.8.8/app", want: reviewport.ErrLaunchURLNotReviewable},
		{name: "empty answer", url: "https://empty.example.edu/app", want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "private", url: "https://private.example.edu", addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "mixed public private", url: "https://mixed.example.edu", addresses: []netip.Addr{publicV4, netip.MustParseAddr("192.168.1.1")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "CGNAT", url: "https://cgnat.example.edu", addresses: []netip.Addr{netip.MustParseAddr("100.64.1.2")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "documentation IPv4", url: "https://docs.example.edu", addresses: []netip.Addr{netip.MustParseAddr("203.0.113.8")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "benchmark IPv4", url: "https://bench.example.edu", addresses: []netip.Addr{netip.MustParseAddr("198.18.0.1")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "loopback IPv6", url: "https://loop.example.edu", addresses: []netip.Addr{netip.MustParseAddr("::1")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "documentation IPv6", url: "https://docs6.example.edu", addresses: []netip.Addr{netip.MustParseAddr("2001:db8::1")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "mapped private IPv4", url: "https://mapped.example.edu", addresses: []netip.Addr{netip.MustParseAddr("::ffff:10.0.0.1")}, want: reviewport.ErrLaunchURLNotReviewable, calls: 1},
		{name: "DNS unavailable", url: "https://down.example.edu", resolver: errors.New("resolver down"), want: reviewport.ErrLaunchURLInspectionUnavailable, calls: 1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			resolver := &deterministicResolver{addresses: testCase.addresses, err: testCase.resolver}
			policy := NewLaunchURLSubmissionPolicy(resolver)
			version, err := policy.Inspect(context.Background(), reviewdomain.LaunchURL(testCase.url))
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Inspect() error = %v, want %v", err, testCase.want)
			}
			if testCase.want == nil && version.String() != PolicyVersion {
				t.Fatalf("Inspect() version = %q, want %q", version, PolicyVersion)
			}
			if resolver.calls != testCase.calls {
				t.Fatalf("resolver calls = %d, want %d", resolver.calls, testCase.calls)
			}
			if testCase.wantHost != "" && resolver.host != testCase.wantHost {
				t.Fatalf("resolver host = %q, want %q", resolver.host, testCase.wantHost)
			}
		})
	}
}

func TestLaunchURLSubmissionPolicy_BR_REV_007_FrozenSpecialUseDomains(t *testing.T) {
	for _, special := range specialUseDomainNames {
		for _, host := range []string{special, "child." + special} {
			t.Run(host, func(t *testing.T) {
				resolver := &deterministicResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
				_, err := NewLaunchURLSubmissionPolicy(resolver).Inspect(context.Background(), reviewdomain.LaunchURL("https://"+host+"/"))
				if !errors.Is(err, reviewport.ErrLaunchURLNotReviewable) || resolver.calls != 0 {
					t.Fatalf("Inspect(%q) = %v, resolver calls %d", host, err, resolver.calls)
				}
			})
		}
	}
}

func TestLaunchURLSubmissionPolicy_BR_REV_007_FrozenSpecialPurposeAddresses(t *testing.T) {
	for _, prefix := range disallowedAddressPrefixes {
		t.Run(prefix.String(), func(t *testing.T) {
			if isAllowedPublicAddress(prefix.Addr()) {
				t.Fatalf("special-purpose prefix %s accepted", prefix)
			}
		})
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !isAllowedPublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("ordinary public address %s rejected", raw)
		}
	}
	for _, raw := range []string{"4000::1", "8000::1", "fec0::1"} {
		if isAllowedPublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("IETF-reserved IPv6 address %s accepted", raw)
		}
	}
}
