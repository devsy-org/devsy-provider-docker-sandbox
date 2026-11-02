package distribution

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateUsesActualBytesAndQualifiedHosts(t *testing.T) {
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "profile.json")
	p := qualifiedProfile()
	p.QualifiedHosts = []string{"linux/amd64", "windows/amd64"}
	delete(p.InventoryStates, "failed")
	delete(p.InventoryStates, "starting")
	writeProfile(t, profilePath, p)
	linux := []byte("synthetic executable one")
	windows := []byte("synthetic executable two")
	for name, data := range map[string][]byte{
		"devsy-provider-docker-sandbox-linux-amd64":       linux,
		"devsy-provider-docker-sandbox-windows-amd64.exe": windows,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := Generate("v1.2.3", directory, profilePath)
	if err != nil {
		t.Fatal(err)
	}
	linuxHash := fmt.Sprintf("%x", sha256.Sum256(linux))
	windowsHash := fmt.Sprintf("%x", sha256.Sum256(windows))
	for _, checksum := range []string{linuxHash, windowsHash} {
		if !strings.Contains(string(metadata.Manifest), "checksum: "+checksum) || !strings.Contains(string(metadata.Checksums), checksum) {
			t.Fatalf("manifest or checksum file lacks actual executable checksum %s", checksum)
		}
	}
	if strings.Contains(string(metadata.Manifest), "darwin/arm64") || strings.Contains(string(metadata.Manifest), "-darwin-arm64") {
		t.Fatal("unqualified host included in release")
	}
	for _, required := range []string{"install: false", "configureDaemon: false", "cancellation: file", "dataPath: /var/lib/devsy/agent", "DOCKER_SANDBOX_PROVIDER:", "DOCKER_HOST: unix:///var/run/docker.sock"} {
		if !strings.Contains(string(metadata.Manifest), required) {
			t.Fatalf("generated manifest lacks %q", required)
		}
	}
	profileBytes, err := os.ReadFile(profilePath)
	if err != nil || string(metadata.Profile) != string(profileBytes) {
		t.Fatal("release profile does not match reviewed source")
	}
}

func TestGenerateRejectsIncompleteRelease(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*profile)
		art     []byte
		wantErr string
	}{
		{"no host", func(p *profile) { p.QualifiedHosts = nil }, nil, "no qualified native hosts"},
		{"no daemon schema", func(p *profile) { p.DaemonSchema = "" }, nil, "unqualified"},
		{"no operation result schema", func(p *profile) { p.OperationResultSchema = "" }, nil, "unqualified"},
		{"no core version", func(p *profile) { p.MinimumDevsyVersion = "" }, nil, "unqualified"},
		{"missing client revision", func(p *profile) { p.QualifiedSBXRevisions["v0.50.1"] = "" }, nil, "no measured revision"},
		{"missing daemon version", func(p *profile) { p.QualifiedDaemonVersions["v0.50.1"] = nil }, nil, "no measured daemon versions"},
		{"missing state", func(p *profile) { delete(p.InventoryStates, "running") }, nil, "running and stopped"},
		{"unknown host", func(p *profile) { p.QualifiedHosts = []string{"darwin/amd64"} }, nil, "unsupported"},
		{"duplicate host", func(p *profile) { p.QualifiedHosts = []string{"linux/amd64", "linux/amd64"} }, []byte("synthetic executable"), "duplicate"},
		{"missing artifact", func(*profile) {}, nil, "artifact"},
		{"empty artifact", func(*profile) {}, []byte{}, "nonempty regular"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "profile.json")
			p := qualifiedProfile()
			tt.mutate(&p)
			writeProfile(t, path, p)
			if tt.art != nil {
				if err := os.WriteFile(filepath.Join(directory, "devsy-provider-docker-sandbox-linux-amd64"), tt.art, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Generate("v1.2.3", directory, path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Generate error = %v; want %q", err, tt.wantErr)
			}
		})
	}
}

func qualifiedProfile() profile {
	return profile{
		MinimumDevsyVersion:     "v1.2.3",
		QualifiedHosts:          []string{"linux/amd64"},
		QualifiedSBXVersions:    []string{"v0.50.1"},
		QualifiedSBXRevisions:   map[string]string{"v0.50.1": "synthetic-test-revision"},
		QualifiedDaemonVersions: map[string][]string{"v0.50.1": {"observed-test-daemon"}},
		DaemonSchema:            "synthetic-test-schema",
		OperationResultSchema:   "synthetic-test-result-schema",
		ShellSettingSchema:      "synthetic-test-setting",
		InventoryStates: map[string]string{
			"running": "running", "stopped": "stopped", "starting": "transitional", "failed": "failed",
		},
	}
}

func writeProfile(t *testing.T, path string, p profile) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
