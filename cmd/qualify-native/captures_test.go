package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/qualify"
)

func TestVerifyChecksFixtureFingerprint(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	data, err := os.ReadFile("qualification/v0.51.0/darwin-arm64/native-evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var ev qualify.Evidence
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	setCandidateToHead(t, &ev)
	fingerprint, err := fixtureFingerprint(ev.Hosts[0].ClientVersion)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "evidence.json")
	args := []string{
		"--evidence", file, "--release-commit", ev.CandidateCommit,
		"--core-ref", ev.CoreCommit, "--version", ev.ProviderVersion,
	}
	writeTestEvidence(t, file, ev)
	if err := verify(args); err == nil || !strings.Contains(err.Error(), "sha256 differs") {
		t.Fatalf("stale fixture fingerprint accepted: %v", err)
	}
	ev.Hosts[0].FixtureProvenanceSHA256 = fingerprint
	writeTestEvidence(t, file, ev)
	if err := verify(args); err != nil {
		t.Fatal(err)
	}
}

func writeTestEvidence(t *testing.T, file string, ev qualify.Evidence) {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setCandidateToHead(t *testing.T, ev *qualify.Evidence) {
	t.Helper()
	head, _, err := gitState()
	if err != nil {
		t.Fatal(err)
	}
	ev.CandidateCommit = head
	for i := range ev.Hosts {
		ev.Hosts[i].ProviderCommit = head
	}
}
