package distribution

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"text/template"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

//go:embed provider.yaml.tmpl
var manifestTemplate string

var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[-+][0-9A-Za-z.-]+)?$`)

type binary struct {
	OS       string
	Arch     string
	Name     string
	Checksum string
}

type profile struct {
	MinimumDevsyVersion     string              `json:"minimum_devsy_version"`
	QualifiedHosts          []string            `json:"qualified_hosts"`
	QualifiedSBXVersions    []string            `json:"qualified_sbx_versions"`
	QualifiedSBXRevisions   map[string]string   `json:"qualified_sbx_revisions"`
	QualifiedDaemonVersions map[string][]string `json:"qualified_daemon_versions"`
	DaemonSchema            string              `json:"daemon_schema"`
	OperationResultSchema   string              `json:"operation_result_schema"`
	InventoryStates         map[string]string   `json:"inventory_states"`
	ShellSettingSchema      string              `json:"shell_setting_schema"`
	DiskSizingSchemas       map[string]string   `json:"disk_sizing_schemas"`
}

type Metadata struct {
	Manifest  []byte
	Checksums []byte
	Profile   []byte
}

func Generate(tag, directory, profilePath string) (Metadata, error) {
	if !releaseTag.MatchString(tag) {
		return Metadata{}, fmt.Errorf("invalid release tag %q", tag)
	}
	profileBytes, err := os.ReadFile(profilePath)
	if err != nil {
		return Metadata{}, fmt.Errorf("read compatibility profile: %w", err)
	}
	var qualified profile
	decoder := json.NewDecoder(bytes.NewReader(profileBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&qualified); err != nil {
		return Metadata{}, fmt.Errorf("decode compatibility profile: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Metadata{}, errors.New("compatibility profile contains trailing JSON")
	}
	if !releaseTag.MatchString(qualified.MinimumDevsyVersion) || len(qualified.QualifiedSBXVersions) == 0 || len(qualified.QualifiedSBXRevisions) == 0 || len(qualified.QualifiedDaemonVersions) == 0 || qualified.DaemonSchema == "" || qualified.OperationResultSchema != compat.OperationResultSchemaV0501 || qualified.ShellSettingSchema != compat.ShellSettingSchemaV0501 || len(qualified.InventoryStates) == 0 {
		return Metadata{}, errors.New("compatibility profile is unqualified: measured core, sbx daemon, operation results, inventory and settings evidence is required")
	}
	for _, version := range qualified.QualifiedSBXVersions {
		if !releaseTag.MatchString(version) {
			return Metadata{}, fmt.Errorf("invalid qualified sbx version %q", version)
		}
		if qualified.QualifiedSBXRevisions[version] == "" {
			return Metadata{}, fmt.Errorf("qualified sbx client %s has no measured revision", version)
		}
		daemonVersions := qualified.QualifiedDaemonVersions[version]
		if len(daemonVersions) == 0 {
			return Metadata{}, fmt.Errorf("qualified sbx client %s has no measured daemon versions", version)
		}
		for _, daemonVersion := range daemonVersions {
			if daemonVersion == "" {
				return Metadata{}, fmt.Errorf("invalid qualified daemon version %q", daemonVersion)
			}
		}
	}
	if len(qualified.QualifiedDaemonVersions) != len(qualified.QualifiedSBXVersions) || len(qualified.QualifiedSBXRevisions) != len(qualified.QualifiedSBXVersions) {
		return Metadata{}, errors.New("qualified daemon versions and client revisions must match qualified client versions")
	}
	states := map[string]bool{}
	for observed, classified := range qualified.InventoryStates {
		if observed == "" {
			return Metadata{}, errors.New("empty observed inventory state")
		}
		switch classified {
		case "running", "stopped", "transitional", "failed":
			states[classified] = true
		default:
			return Metadata{}, fmt.Errorf("invalid inventory state classification %q", classified)
		}
	}
	if !states["running"] || !states["stopped"] {
		return Metadata{}, errors.New("compatibility profile requires observed running and stopped inventory states")
	}
	platforms := map[string]binary{
		"darwin/arm64":  {OS: "darwin", Arch: "arm64"},
		"windows/amd64": {OS: "windows", Arch: "amd64"},
		"linux/amd64":   {OS: "linux", Arch: "amd64"},
		"linux/arm64":   {OS: "linux", Arch: "arm64"},
	}
	if len(qualified.QualifiedHosts) == 0 {
		return Metadata{}, errors.New("compatibility profile contains no qualified native hosts")
	}
	if len(qualified.QualifiedSBXVersions) != 1 {
		return Metadata{}, errors.New("disk sizing requires one measured sbx client version")
	}
	seen := make(map[string]bool, len(qualified.QualifiedHosts))
	binaries := make([]binary, 0, len(qualified.QualifiedHosts))
	var checksums bytes.Buffer
	for _, host := range qualified.QualifiedHosts {
		item, ok := platforms[host]
		if !ok || seen[host] {
			return Metadata{}, fmt.Errorf("unsupported or duplicate qualified host %q", host)
		}
		seen[host] = true
		version := qualified.QualifiedSBXVersions[0]
		expected := compat.MeasuredDiskSizingSchema(host, version, qualified.QualifiedSBXRevisions[version])
		if expected == "" || qualified.DiskSizingSchemas[host] != expected {
			return Metadata{}, fmt.Errorf("unqualified disk sizing schema for host %q", host)
		}
		item.Name = "devsy-provider-docker-sandbox-" + item.OS + "-" + item.Arch
		if item.OS == "windows" {
			item.Name += ".exe"
		}
		item.Checksum, err = fileChecksum(filepath.Join(directory, item.Name))
		if err != nil {
			return Metadata{}, fmt.Errorf("artifact %s: %w", item.Name, err)
		}
		fmt.Fprintf(&checksums, "%s  %s\n", item.Checksum, item.Name)
		binaries = append(binaries, item)
	}
	if len(qualified.DiskSizingSchemas) != len(qualified.QualifiedHosts) {
		return Metadata{}, errors.New("qualified hosts require exact measured disk sizing schemas")
	}
	parsed, err := template.New("provider").Option("missingkey=error").Parse(manifestTemplate)
	if err != nil {
		return Metadata{}, err
	}
	var manifest bytes.Buffer
	if err := parsed.Execute(&manifest, struct {
		Tag      string
		Binaries []binary
	}{Tag: tag, Binaries: binaries}); err != nil {
		return Metadata{}, err
	}
	return Metadata{Manifest: manifest.Bytes(), Checksums: checksums.Bytes(), Profile: profileBytes}, nil
}

func fileChecksum(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("artifact must be a nonempty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
