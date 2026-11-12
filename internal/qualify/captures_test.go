package qualify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const captureData = "capture"

func TestVerifyObservedCaptures(t *testing.T) {
	for _, version := range []string{"v0.50.1", "v0.51.0"} {
		t.Run(version, func(t *testing.T) {
			directory := filepath.Join("..", "sbx", "testdata", version)
			fingerprint, err := VerifyCaptures(directory)
			if err != nil {
				t.Fatal(err)
			}
			data := readProvenance(t, directory)
			if fingerprint != SHA256Hex(data) {
				t.Fatalf("provenance fingerprint = %s, %v", fingerprint, err)
			}
		})
	}
}

func readProvenance(t *testing.T, directory string) []byte {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	data, err := root.ReadFile("provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVerifyCapturesRejectsIncompleteOrChangedFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]capture) []capture
		file string
		want string
	}{
		{"too few", func(c []capture) []capture { return c[:3] }, captureData, "at least 4"},
		{"missing failure", func(c []capture) []capture { c[0].Kind = ""; return c }, captureData, "kind failure"},
		{"missing stop", func(c []capture) []capture { c[1].Kind = ""; return c }, captureData, "kind stop_result"},
		{"missing remove", func(c []capture) []capture { c[2].Kind = ""; return c }, captureData, "kind rm_result"},
		{
			"unsafe name",
			func(c []capture) []capture { c[0].File = "../fixture.txt"; return c },
			captureData, "invalid filename",
		},
		{
			"wrong extension",
			func(c []capture) []capture { c[0].File = "fixture.stderr"; return c },
			captureData, "invalid filename",
		},
		{"invalid digest", func(c []capture) []capture { c[0].SHA256 = "invalid"; return c }, captureData, "sha256"},
		{"missing file", func(c []capture) []capture { c[0].File = "missing.txt"; return c }, captureData, "missing.txt"},
		{"changed bytes", nil, "changed", "differs from provenance"},
		{"empty file", nil, "", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			captures := captureEntries()
			if tc.edit != nil {
				captures = tc.edit(captures)
			}
			writeCaptureFile(t, directory, "fixture.txt", []byte(tc.file))
			data, err := json.Marshal(map[string]any{"captures": captures})
			if err != nil {
				t.Fatal(err)
			}
			writeCaptureFile(t, directory, "provenance.json", data)
			_, err = VerifyCaptures(directory)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verification error = %v, want %s", err, tc.want)
			}
		})
	}
}

func captureEntries() []capture {
	var captures []capture
	for _, kind := range []string{"failure", "stop_result", "rm_result", "inventory"} {
		captures = append(captures, capture{"fixture.txt", SHA256Hex([]byte(captureData)), kind})
	}
	return captures
}

func writeCaptureFile(t *testing.T, directory, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
