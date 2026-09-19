package domain

import (
	"net"
	"net/url"
	"strings"
)

const MaximumLaunchURLBytes = 2048

type LaunchURL struct {
	value         string
	developmental bool
}

func NewLaunchURL(value string) (LaunchURL, error) {
	if len(value) == 0 || len(value) > MaximumLaunchURLBytes {
		return LaunchURL{}, ErrInvalidApplicationLaunchURL
	}

	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return LaunchURL{}, ErrInvalidApplicationLaunchURL
	}

	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return LaunchURL{value: value}, nil
	case "http":
		if !isDevelopmentHost(parsed.Hostname()) {
			return LaunchURL{}, ErrInvalidApplicationLaunchURL
		}
		return LaunchURL{value: value, developmental: true}, nil
	default:
		return LaunchURL{}, ErrInvalidApplicationLaunchURL
	}
}

func (launchURL LaunchURL) String() string        { return launchURL.value }
func (launchURL LaunchURL) IsDevelopmental() bool { return launchURL.developmental }
func (launchURL LaunchURL) valid() bool {
	validated, err := NewLaunchURL(launchURL.value)
	return err == nil && validated == launchURL
}

func isDevelopmentHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.To4() != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
