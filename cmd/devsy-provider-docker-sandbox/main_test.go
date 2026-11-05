package main

import (
	"bytes"
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
		{"unqualified init", unqualified, "init", true},
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
