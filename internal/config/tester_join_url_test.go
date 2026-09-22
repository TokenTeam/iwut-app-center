package config

import (
	"errors"
	"strings"
	"testing"
)

func TestTesterJoinURLPrefixDefaultAndCustom(t *testing.T) {
	values := requiredValues()
	cfg, err := Load(lookupFrom(values))
	if err != nil || cfg.TesterJoinURLPrefix != DefaultTesterJoinURLPrefix {
		t.Fatal("missing default mock prefix")
	}
	for _, prefix := range []string{"https://app.example/tester/join", "http://localhost:8080/dev/join/", "https://example.invalid/中文/%2F/path/", "http://[::1]:8080/"} {
		values[TesterJoinURLPrefixEnv] = prefix
		cfg, err = Load(lookupFrom(values))
		if err != nil || cfg.TesterJoinURLPrefix != prefix {
			t.Fatal("custom prefix not preserved")
		}
	}
}
func TestTesterJoinURLPrefixExplicitInvalidNeverFallsBack(t *testing.T) {
	for _, prefix := range []string{"", " ", " https://app.example/join", "https://app.example/join ", "/join", "ftp://host/join", "https:///join", "https://", "https://user@host/join", "https://host/join?", "https://host/join?a=b", "https://host/join#", "https://host/join#x", "https://host/%zz", "https://host:bad/join", "https://host/join\n", "https://host/path with spaces"} {
		t.Run(prefix, func(t *testing.T) {
			values := requiredValues()
			values[TesterJoinURLPrefixEnv] = prefix
			cfg, err := Load(lookupFrom(values))
			if !errors.Is(err, ErrInvalidConfiguration) || cfg.TesterJoinURLPrefix != "" || !strings.Contains(err.Error(), TesterJoinURLPrefixEnv) {
				t.Fatal("invalid explicit config accepted or wrong error")
			}
			if strings.TrimSpace(prefix) != "" && strings.Contains(err.Error(), prefix) {
				t.Fatal("bad prefix echoed")
			}
		})
	}
}
