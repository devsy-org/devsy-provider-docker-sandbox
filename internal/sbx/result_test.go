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
		{"stop-result.json", testStopped},
		{"rm-result.json", "removed"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			if err := CheckResult(
				readMeasuredFixture(t, tc.file),
				"devsy-qual-20261104",
				tc.action,
			); err != nil {
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
		{"wrong name", stopped, "other", testStopped, true},
		{"wrong action", stopped, testOwned, "removed", true},
		{"wrong resource", `[{"action":"stopped","resource":"workspace","name":"owned"}]`, testOwned, testStopped, true},
		{"extra target", `[{"action":"stopped","resource":"sandbox","name":"owned"},{"actio` +
			`n":"stopped","resource":"sandbox","name":"other"}]`, testOwned, testStopped, true},
		{
			"duplicate target", `[` + stopped[1:len(stopped)-1] + `,` + stopped[1:len(stopped)-1] + `]`,
			testOwned,
			testStopped, true,
		},
		{"pending", `[{"action":"pending","resource":"sandbox","name":"owned"}]`, testOwned, testStopped, true},
		{
			"error", `[{"action":"stopped","resource":"sandbox","name":"owned","error":"failed"}]`,
			testOwned,
			testStopped, true,
		},
		{
			"extra property", `[{"action":"stopped","resource":"sandbox","name":"owned","status":"done"}]`,
			testOwned,
			testStopped, true,
		},
		{"missing property", `[{"action":"stopped","name":"owned"}]`, testOwned, testStopped, true},
		{
			"duplicate property", `[{"action":"stopped","action":"stopped","resource":"sandbox","name":"owned"}]`,
			testOwned,
			testStopped, true,
		},
		{"null property", `[{"action":"stopped","resource":"sandbox","name":null}]`, testOwned, testStopped, true},
		{"empty", `[]`, testOwned, testStopped, true},
		{"null", testNull, testOwned, testStopped, true},
		{"malformed", `[{"action":"stopped"`, testOwned, testStopped, true},
		{"trailing", stopped + ` true`, testOwned, testStopped, true},
		{"bad expectation", stopped, testOwned, "pending", true},
		{"empty target", stopped, "", testStopped, true},
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
			if !errors.As(err, &tagged) || tagged.Code != testSchema {
				t.Fatalf("expected schema rejection, got %v", err)
			}
		})
	}
}
