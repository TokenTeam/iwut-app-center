// Package preflight implements the submission-time launch URL address policy.
// It deliberately performs DNS resolution only: it never connects to, fetches
// from, or follows redirects at the submitted URL.
package preflight

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"golang.org/x/net/idna"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
)

const PolicyVersion = "submit-v1"

// Resolver is the narrow DNS dependency used by the policy. Production binds
// net.DefaultResolver; tests can supply deterministic A/AAAA answers.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type LaunchURLSubmissionPolicy struct {
	resolver Resolver
}

var _ reviewport.LaunchURLSubmissionPolicy = (*LaunchURLSubmissionPolicy)(nil)

func NewLaunchURLSubmissionPolicy(resolver Resolver) *LaunchURLSubmissionPolicy {
	return &LaunchURLSubmissionPolicy{resolver: resolver}
}

func NewNetResolver() *net.Resolver { return net.DefaultResolver }

func (policy *LaunchURLSubmissionPolicy) Inspect(
	ctx context.Context,
	launchURL reviewdomain.LaunchURL,
) (reviewdomain.PreflightPolicyVersion, error) {
	if policy == nil || policy.resolver == nil {
		return "", errors.Join(reviewport.ErrLaunchURLInspectionUnavailable, errors.New("DNS resolver is unavailable"))
	}
	parsed, err := url.Parse(string(launchURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Hostname() == "" {
		return "", reviewport.ErrLaunchURLNotReviewable
	}

	// BR-REV-007 requires an A/AAAA DNS result. A literal IP bypasses that
	// requirement, so it is not reviewable even when the address is public.
	rawHost := parsed.Hostname()
	if _, err := netip.ParseAddr(rawHost); err == nil {
		return "", reviewport.ErrLaunchURLNotReviewable
	}
	host, err := normalizeDomainName(rawHost)
	if err != nil || isReservedHostname(host) {
		return "", reviewport.ErrLaunchURLNotReviewable
	}

	addresses, err := policy.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", errors.Join(reviewport.ErrLaunchURLInspectionUnavailable, err)
	}
	if len(addresses) == 0 {
		return "", reviewport.ErrLaunchURLNotReviewable
	}
	for _, address := range addresses {
		if !isAllowedPublicAddress(address) {
			return "", reviewport.ErrLaunchURLNotReviewable
		}
	}

	version, err := reviewdomain.NewPreflightPolicyVersion(PolicyVersion)
	if err != nil {
		return "", errors.Join(reviewport.ErrLaunchURLInspectionUnavailable, err)
	}
	return version, nil
}

func isReservedHostname(host string) bool {
	for _, reserved := range specialUseDomainNames {
		if host == reserved || strings.HasSuffix(host, "."+reserved) {
			return true
		}
	}
	return false
}

func normalizeDomainName(host string) (string, error) {
	if host == "" || strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return "", errors.New("invalid DNS label")
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
		if host == "" || strings.HasSuffix(host, ".") {
			return "", errors.New("invalid DNS root suffix")
		}
	}
	alabel, err := idna.Lookup.ToASCII(host)
	if err != nil || alabel == "" || strings.HasPrefix(alabel, ".") ||
		strings.HasSuffix(alabel, ".") || strings.Contains(alabel, "..") {
		return "", errors.New("invalid IDNA domain")
	}
	return strings.ToLower(alabel), nil
}

// specialUseDomainNames freezes the IANA Special-Use Domain Names registry at
// its 2026-05-22 revision. Registry semantics apply to each name and all of its
// subdomains. A registry update requires a new PolicyVersion.
var specialUseDomainNames = []string{
	"alt",
	"6tisch.arpa",
	"eap.arpa",
	"eap-noob.arpa",
	"home.arpa",
	"10.in-addr.arpa",
	"254.169.in-addr.arpa",
	"16.172.in-addr.arpa",
	"17.172.in-addr.arpa",
	"18.172.in-addr.arpa",
	"19.172.in-addr.arpa",
	"20.172.in-addr.arpa",
	"21.172.in-addr.arpa",
	"22.172.in-addr.arpa",
	"23.172.in-addr.arpa",
	"24.172.in-addr.arpa",
	"25.172.in-addr.arpa",
	"26.172.in-addr.arpa",
	"27.172.in-addr.arpa",
	"28.172.in-addr.arpa",
	"29.172.in-addr.arpa",
	"30.172.in-addr.arpa",
	"31.172.in-addr.arpa",
	"170.0.0.192.in-addr.arpa",
	"171.0.0.192.in-addr.arpa",
	"168.192.in-addr.arpa",
	"8.e.f.ip6.arpa",
	"9.e.f.ip6.arpa",
	"a.e.f.ip6.arpa",
	"b.e.f.ip6.arpa",
	"ipv4only.arpa",
	"resolver.arpa",
	"service.arpa",
	"example",
	"example.com",
	"example.net",
	"example.org",
	"invalid",
	"local",
	"localhost",
	"onion",
	"test",
}

var disallowedAddressPrefixes = []netip.Prefix{
	// IPv4 special-use, private, loopback, link-local, documentation,
	// benchmarking, shared CGNAT, multicast and reserved space.
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// IPv6 special-use, translation, discard, benchmarking,
	// documentation, unique-local, link-local and multicast space.
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::ffff:0:0/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

var ipv6GlobalUnicastPrefix = netip.MustParsePrefix("2000::/3")

func isAllowedPublicAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	if address.Is4In6() {
		address = address.Unmap()
	}
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	// netip.IsGlobalUnicast follows address-type semantics and can classify
	// IETF-reserved IPv6 space as unicast. submit-v1 only accepts the IANA
	// Global Unicast allocation and then applies the frozen special-use denylist.
	if address.Is6() && !ipv6GlobalUnicastPrefix.Contains(address) {
		return false
	}
	for _, prefix := range disallowedAddressPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
