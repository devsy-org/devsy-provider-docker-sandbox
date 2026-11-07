package main

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func TestWarnUnqualifiedHost(t *testing.T) {
	unqualified := compat.Profile{QualifiedHosts: []string{"plan9/mips"}}
	qualified := compat.Profile{QualifiedHosts: []string{runtime.GOOS + "/" + runtime.GOARCH}}
	for _, tc := range []struct {
		name    string
		profile compat.Profile
		verb    string
		warns   bool
	}{
		{"unqualified init", unqualified, initVerb, true},
		{"unqualified create", unqualified, "create", true},
		{"unqualified start", unqualified, "start", true},
		{"unqualified status", unqualified, "status", false},
		{"qualified init", qualified, "init", false},
		{"qualified create", qualified, "create", false},
		{"qualified start", qualified, "start", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			warnUnqualifiedHost(&out, tc.profile, tc.verb)
			if (out.Len() != 0) != tc.warns {
				t.Fatalf("output = %q, want warning %t", out.String(), tc.warns)
			}
			if tc.warns && (!strings.HasPrefix(out.String(), "warning: host ") ||
				strings.Count(out.String(), "\n") != 1 ||
				!strings.Contains(out.String(), "no native qualification evidence")) {
				t.Fatalf("unexpected warning %q", out.String())
			}
		})
	}
}

func requireBelowMinimum(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "E_CONFIG_DRIFT") ||
		!strings.Contains(err.Error(), "below the minimum") {
		t.Fatalf("got %v, want E_CONFIG_DRIFT below the minimum", err)
	}
}

func TestLoadConfigEnforcesMinimumsOnlyWhenAdmittingASpec(t *testing.T) {
	t.Setenv("SBX_PATH", os.Args[0])
	t.Setenv("SBX_DOCKER_DISK", "6656m")
	t.Setenv("SBX_ROOT_DISK", "7g")
	for _, verb := range []string{initVerb, createVerb} {
		_, err := loadConfig(verb, "")
		requireBelowMinimum(t, err)
	}
	for _, verb := range []string{
		startVerb, "stop", deleteVerb, statusVerb, "describe", "command", recoverVerb,
	} {
		t.Run(verb, func(t *testing.T) {
			cfg, err := loadConfig(verb, "")
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if cfg.Spec.DockerDiskBytes != 6656<<20 || cfg.Spec.RootDiskBytes != 7<<30 {
				t.Fatalf("unexpected spec %+v", cfg.Spec)
			}
		})
	}
	for _, verb := range []string{deleteVerb, recoverVerb} {
		if _, err := loadConfig(verb, "key"); err != nil {
			t.Fatalf("%s with machine key: %v", verb, err)
		}
	}
}

func TestLoadConfigStillValidatesSizeSyntaxForExistingMachines(t *testing.T) {
	t.Setenv("SBX_PATH", os.Args[0])
	t.Setenv("SBX_DOCKER_DISK", "6656M")
	for _, verb := range []string{startVerb, "stop", deleteVerb, statusVerb, "command"} {
		if _, err := loadConfig(verb, ""); err == nil ||
			!strings.Contains(err.Error(), "E_CONFIG_DRIFT") {
			t.Fatalf("%s: got %v, want E_CONFIG_DRIFT", verb, err)
		}
	}
}
