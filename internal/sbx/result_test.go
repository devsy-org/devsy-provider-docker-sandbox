package sbx

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func TestObservedOperationResults(t *testing.T) {
	for _, tc := range []struct {
		file, action string
	}{
		{"stop-result.json", "stopped"},
		{"rm-result.json", "removed"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			if err := CheckResult(readMeasuredFixture(t, tc.file), "devsy-qual-20261104", tc.action); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckResult(t *testing.T) {
	const stopped = `[{"action":"stopped","resource":"sandbox","name":"owned"}]`
	for _, test := range []struct {
		name, json, target, action string
		wantError                  bool
	}{
		{"wrong name", stopped, "other", "stopped", true},
		{"wrong action", stopped, "owned", "removed", true},
		{"wrong resource", `[{"action":"stopped","resource":"workspace","name":"owned"}]`, "owned", "stopped", true},
		{"extra target", `[{"action":"stopped","resource":"sandbox","name":"owned"},{"action":"stopped","resource":"sandbox","name":"other"}]`, "owned", "stopped", true},
		{"duplicate target", `[` + stopped[1:len(stopped)-1] + `,` + stopped[1:len(stopped)-1] + `]`, "owned", "stopped", true},
		{"pending", `[{"action":"pending","resource":"sandbox","name":"owned"}]`, "owned", "stopped", true},
		{"error", `[{"action":"stopped","resource":"sandbox","name":"owned","error":"failed"}]`, "owned", "stopped", true},
		{"extra property", `[{"action":"stopped","resource":"sandbox","name":"owned","status":"done"}]`, "owned", "stopped", true},
		{"missing property", `[{"action":"stopped","name":"owned"}]`, "owned", "stopped", true},
		{"duplicate property", `[{"action":"stopped","action":"stopped","resource":"sandbox","name":"owned"}]`, "owned", "stopped", true},
		{"null property", `[{"action":"stopped","resource":"sandbox","name":null}]`, "owned", "stopped", true},
		{"empty", `[]`, "owned", "stopped", true},
		{"null", `null`, "owned", "stopped", true},
		{"malformed", `[{"action":"stopped"`, "owned", "stopped", true},
		{"trailing", stopped + ` true`, "owned", "stopped", true},
		{"bad expectation", stopped, "owned", "pending", true},
		{"empty target", stopped, "", "stopped", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := CheckResult([]byte(test.json), test.target, test.action)
			if !test.wantError {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var tagged *compat.Error
			if !errors.As(err, &tagged) || tagged.Code != "E_SBX_SCHEMA" {
				t.Fatalf("expected schema rejection, got %v", err)
			}
		})
	}
}
