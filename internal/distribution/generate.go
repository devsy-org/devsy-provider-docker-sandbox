package distribution

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"text/template"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

//go:embed provider.yaml.tmpl
var manifestTemplate string

var releaseTag = regexp.MustCompile(
	`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[-+][0-9A-Za-z.-]+)?$`,
)

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
	profileBytes, err := fs.ReadFile(
		os.DirFS(filepath.Dir(profilePath)),
		filepath.Base(profilePath),
	)
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
	if err := validateProfile(qualified); err != nil {
		return Metadata{}, err
	}
	binaries, err := collectBinaries(qualified, directory)
	if err != nil {
		return Metadata{}, err
	}
	manifest, err := renderManifest(tag, binaries)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{
		Manifest:  manifest,
		Checksums: renderChecksums(binaries),
		Profile:   profileBytes,
	}, nil
}

func validateProfile(qualified profile) error {
	if err := validateProfileFields(qualified); err != nil {
		return err
	}
	if err := validateVersions(qualified); err != nil {
		return err
	}
	return validateStates(qualified)
}

func validateProfileFields(qualified profile) error {
	if !releaseTag.MatchString(qualified.MinimumDevsyVersion) ||
		len(qualified.QualifiedSBXVersions) == 0 ||
		len(qualified.QualifiedSBXRevisions) == 0 ||
		len(qualified.QualifiedDaemonVersions) == 0 ||
		qualified.DaemonSchema == "" ||
		!validSchemas(qualified) ||
		len(qualified.InventoryStates) == 0 {
		return errors.New(
			"compatibility profile is unqualified: measured core, sbx daemon, operation results, " +
				"inventory and settings evidence is required",
		)
	}
	return nil
}

func validSchemas(p profile) bool {
	return p.OperationResultSchema == compat.OperationResultSchemaV0501 &&
		p.ShellSettingSchema == compat.ShellSettingSchemaV0501
}

func validateVersions(qualified profile) error {
	for _, version := range qualified.QualifiedSBXVersions {
		if err := validateVersion(qualified, version); err != nil {
			return err
		}
	}
	if len(qualified.QualifiedDaemonVersions) != len(qualified.QualifiedSBXVersions) ||
		len(qualified.QualifiedSBXRevisions) != len(qualified.QualifiedSBXVersions) {
		return errors.New(
			"qualified daemon versions and client revisions must match qualified client versions",
		)
	}
	return nil
}

func validateVersion(qualified profile, version string) error {
	if !releaseTag.MatchString(version) {
		return fmt.Errorf("invalid qualified sbx version %q", version)
	}
	if qualified.QualifiedSBXRevisions[version] == "" {
		return fmt.Errorf(
			"qualified sbx client %s has no measured revision",
			version,
		)
	}
	daemonVersions := qualified.QualifiedDaemonVersions[version]
	if len(daemonVersions) == 0 {
		return fmt.Errorf(
			"qualified sbx client %s has no measured daemon versions",
			version,
		)
	}
	for _, daemonVersion := range daemonVersions {
		if daemonVersion == "" {
			return fmt.Errorf("invalid qualified daemon version %q", daemonVersion)
		}
	}
	return nil
}

func validateStates(qualified profile) error {
	states := map[string]bool{}
	for observed, classified := range qualified.InventoryStates {
		if observed == "" {
			return errors.New("empty observed inventory state")
		}
		switch classified {
		case "running", "stopped", "transitional", "failed":
			states[classified] = true
		default:
			return fmt.Errorf("invalid inventory state classification %q", classified)
		}
	}
	if !states["running"] || !states["stopped"] {
		return errors.New(
			"compatibility profile requires observed running and stopped inventory states",
		)
	}
	return nil
}

var platforms = []binary{
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

func (b binary) host() string { return b.OS + "/" + b.Arch }

func collectBinaries(qualified profile, directory string) ([]binary, error) {
	if err := validateQualifiedHosts(qualified); err != nil {
		return nil, err
	}
	binaries := make([]binary, 0, len(platforms))
	for _, platform := range platforms {
		item, err := collectBinary(directory, platform)
		if err != nil {
			return nil, err
		}
		binaries = append(binaries, item)
	}
	return binaries, nil
}

func supportedHost(host string) bool {
	return slices.ContainsFunc(platforms, func(b binary) bool { return b.host() == host })
}

func validateQualifiedHosts(qualified profile) error {
	if len(qualified.QualifiedHosts) == 0 {
		return errors.New("compatibility profile contains no qualified native hosts")
	}
	if len(qualified.QualifiedSBXVersions) != 1 {
		return errors.New("disk sizing requires one measured sbx client version")
	}
	version := qualified.QualifiedSBXVersions[0]
	seen := make(map[string]bool, len(qualified.QualifiedHosts))
	for _, host := range qualified.QualifiedHosts {
		if seen[host] || !supportedHost(host) {
			return fmt.Errorf("unsupported or duplicate qualified host %q", host)
		}
		seen[host] = true
		expected := compat.MeasuredDiskSizingSchema(
			host,
			version,
			qualified.QualifiedSBXRevisions[version],
		)
		if expected == "" || qualified.DiskSizingSchemas[host] != expected {
			return fmt.Errorf("unqualified disk sizing schema for host %q", host)
		}
	}
	return validateDiskSizingHosts(qualified)
}

func validateDiskSizingHosts(qualified profile) error {
	for _, host := range slices.Sorted(maps.Keys(qualified.DiskSizingSchemas)) {
		if !slices.Contains(qualified.QualifiedHosts, host) {
			return fmt.Errorf("disk sizing schema for host %q is not in qualified_hosts", host)
		}
	}
	return nil
}

func collectBinary(directory string, item binary) (binary, error) {
	item.Name = "devsy-provider-docker-sandbox-" + item.OS + "-" + item.Arch
	if item.OS == "windows" {
		item.Name += ".exe"
	}
	checksum, err := fileChecksum(filepath.Join(directory, item.Name))
	if err != nil {
		return binary{}, fmt.Errorf("artifact %s: %w", item.Name, err)
	}
	item.Checksum = checksum
	return item, nil
}

type manifestData struct {
	Tag      string
	Binaries []binary
}

func renderManifest(tag string, binaries []binary) ([]byte, error) {
	parsed, err := template.New("provider").Option("missingkey=error").Parse(manifestTemplate)
	if err != nil {
		return nil, err
	}
	var manifest bytes.Buffer
	if err := parsed.Execute(&manifest, manifestData{Tag: tag, Binaries: binaries}); err != nil {
		return nil, err
	}
	return manifest.Bytes(), nil
}

func renderChecksums(binaries []binary) []byte {
	var checksums bytes.Buffer
	for _, item := range binaries {
		fmt.Fprintf(&checksums, "%s  %s\n", item.Checksum, item.Name)
	}
	return checksums.Bytes()
}

func fileChecksum(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("artifact must be a nonempty regular file")
	}
	file, err := os.DirFS(filepath.Dir(path)).Open(filepath.Base(path))
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
