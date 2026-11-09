package distribution

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

const (
	unqualified = "unqualified"
	errArtifact = "artifact"
	darwinHost  = "darwin/arm64"
	sbxVersion  = "v0.51.0"
)

var artifactNames = map[string]string{
	"linux/amd64":   "devsy-provider-docker-sandbox-linux-amd64",
	"linux/arm64":   "devsy-provider-docker-sandbox-linux-arm64",
	"darwin/arm64":  "devsy-provider-docker-sandbox-darwin-arm64",
	"windows/amd64": "devsy-provider-docker-sandbox-windows-amd64.exe",
}

func writeArtifacts(t *testing.T, directory string, skip string) map[string][]byte {
	t.Helper()
	contents := make(map[string][]byte, len(artifactNames))
	for host, name := range artifactNames {
		if host == skip {
			continue
		}
		contents[host] = []byte("synthetic executable " + host)
		if err := os.WriteFile(filepath.Join(directory, name), contents[host], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return contents
}

func TestGenerateEmitsAllHostsWithActualBytes(t *testing.T) {
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "profile.json")
	writeProfile(t, profilePath, qualifiedProfile())
	contents := writeArtifacts(t, directory, "")
	metadata, err := Generate("v1.2.3", directory, profilePath)
	if err != nil {
		t.Fatal(err)
	}
	checkGeneratedMetadata(t, metadata, contents, directory)
}

func checkGeneratedMetadata(
	t *testing.T,
	metadata Metadata,
	contents map[string][]byte,
	directory string,
) {
	t.Helper()
	for host, name := range artifactNames {
		checksum := fmt.Sprintf("%x", sha256.Sum256(contents[host]))
		if !strings.Contains(string(metadata.Manifest), "checksum: "+checksum) ||
			!strings.Contains(string(metadata.Checksums), checksum+"  "+name+"\n") ||
			!strings.Contains(string(metadata.Manifest), "name: "+name+"\n") {
			t.Fatalf("manifest or checksum file lacks %s checksum %s", host, checksum)
		}
	}
	checkManifestFields(t, metadata.Manifest)
	profileBytes, err := fs.ReadFile(os.DirFS(directory), "profile.json")
	if err != nil || string(metadata.Profile) != string(profileBytes) {
		t.Fatal("release profile does not match reviewed source")
	}
}

func checkManifestFields(t *testing.T, manifest []byte) {
	t.Helper()
	for _, required := range []string{
		"install: false", "configureDaemon: false", "cancellation: file",
		"dataPath: /var/lib/devsy/agent", "DOCKER_SANDBOX_PROVIDER:", "DOCKER_HOST: unix:///var/run/docker.sock",
	} {
		if !strings.Contains(string(manifest), required) {
			t.Fatalf("generated manifest lacks %q", required)
		}
	}
}

func TestGenerateRejectsIncompleteRelease(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*profile)
		skip    string
		empty   bool
		wantErr string
	}{
		{"no host", func(p *profile) { p.QualifiedHosts = nil }, "", false, "no qualified native hosts"},
		{"no daemon schema", func(p *profile) { p.DaemonSchema = "" }, "", false, unqualified},
		{"no operation result schema", func(p *profile) { p.OperationResultSchema = "" }, "", false, unqualified},
		{"missing disk sizing schema", func(p *profile) { p.DiskSizingSchemas = nil }, "", false, "unqualified disk sizing"},
		{"unmeasured disk sizing schema", func(p *profile) {
			p.DiskSizingSchemas[darwinHost] = "unmeasured"
		}, "", false, "unqualified disk sizing"},
		{"disk sizing schema for unqualified host", func(p *profile) {
			p.DiskSizingSchemas["linux/amd64"] = "unmeasured"
		}, "", false, "not in qualified_hosts"},
		{"no core version", func(p *profile) { p.MinimumDevsyVersion = "" }, "", false, unqualified},
		{"missing client revision", func(p *profile) {
			p.QualifiedSBXRevisions[sbxVersion] = ""
		}, "", false, "no measured revision"},
		{"missing daemon version", func(p *profile) {
			p.QualifiedDaemonVersions[sbxVersion] = nil
		}, "", false, "no measured daemon versions"},
		{"missing state", func(p *profile) { delete(p.InventoryStates, "running") }, "", false, "running and stopped"},
		{"unknown host", func(p *profile) { p.QualifiedHosts = []string{"darwin/amd64"} }, "", false, "unsupported"},
		{"duplicate host", func(p *profile) {
			p.QualifiedHosts = []string{darwinHost, darwinHost}
		}, "", false, "duplicate"},
		{"missing linux amd64 artifact", func(*profile) {}, "linux/amd64", false, errArtifact},
		{"missing linux arm64 artifact", func(*profile) {}, "linux/arm64", false, errArtifact},
		{"missing darwin arm64 artifact", func(*profile) {}, darwinHost, false, errArtifact},
		{"missing windows amd64 artifact", func(*profile) {}, "windows/amd64", false, errArtifact},
		{"empty artifact", func(*profile) {}, "", true, "nonempty regular"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "profile.json")
			p := qualifiedProfile()
			tt.mutate(&p)
			writeProfile(t, path, p)
			writeArtifacts(t, directory, tt.skip)
			if tt.empty {
				if err := os.WriteFile(
					filepath.Join(directory, artifactNames["windows/amd64"]),
					nil,
					0o600,
				); err != nil {
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
		MinimumDevsyVersion:  "v1.2.3",
		QualifiedHosts:       []string{darwinHost},
		QualifiedSBXVersions: []string{sbxVersion},
		QualifiedSBXRevisions: map[string]string{
			sbxVersion: "7d55286de0c775148b985ed349f760f1eba06203",
		},
		QualifiedDaemonVersions: map[string][]string{sbxVersion: {sbxVersion}},
		DaemonSchema:            "v0.51.0/server-running",
		OperationResultSchema:   compat.OperationResultSchema,
		ShellSettingSchema:      compat.ShellSettingSchema,
		DiskSizingSchemas: map[string]string{
			darwinHost: compat.DiskSizingSchemaDarwinARM64,
		},
		InventoryStates: map[string]string{
			"running": "running", "stopped": "stopped",
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
