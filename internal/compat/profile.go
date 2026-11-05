package compat

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed profile.json
var profileFS embed.FS

const (
	ShellSettingSchemaV0501          = "v0.50.1/effective-shell-setting"
	OperationResultSchemaV0501       = "v0.50.1/action-resource-name"
	DiskSizingSchemaDarwinARM64V0501 = "v0.50.1/darwin-arm64/3-percent-overhead"
)

type Profile struct {
	MinimumDevsyVersion     string              `json:"minimum_devsy_version"`
	QualifiedHosts          []string            `json:"qualified_hosts"`
	QualifiedSBXVersions    []string            `json:"qualified_sbx_versions"`
	QualifiedSBXRevisions   map[string]string   `json:"qualified_sbx_revisions"`
	QualifiedDaemonVersions map[string][]string `json:"qualified_daemon_versions"`
	DaemonSchema            string              `json:"daemon_schema"`
	InventoryStates         map[string]string   `json:"inventory_states"`
	ShellSettingSchema      string              `json:"shell_setting_schema"`
	OperationResultSchema   string              `json:"operation_result_schema"`
	DiskSizingSchemas       map[string]string   `json:"disk_sizing_schemas"`
}

type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

func Fail(
	code, format string,
	args ...any,
) error {
	return &Error{code, fmt.Sprintf(format, args...)}
}

func Wrap(code string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	return Fail(code, "%v", err)
}

func Load() (Profile, error) {
	data, err := profileFS.ReadFile("profile.json")
	if err != nil {
		return Profile{}, Wrap("E_SBX_VERSION", err)
	}
	var p Profile
	if err = json.Unmarshal(data, &p); err != nil {
		return p, Wrap("E_SBX_VERSION", err)
	}
	return p, nil
}

func currentHost() string { return runtime.GOOS + "/" + runtime.GOARCH }

func (p Profile) HostQualified() bool {
	return slices.Contains(p.QualifiedHosts, currentHost())
}

func (p Profile) Host() error {
	if !p.HostQualified() {
		return nil
	}
	return hostVersion()
}

func (p Profile) HostWarning() string {
	if p.HostQualified() {
		return ""
	}
	qualified := "none"
	if len(p.QualifiedHosts) != 0 {
		qualified = strings.Join(p.QualifiedHosts, ", ")
	}
	return fmt.Sprintf(
		"host %s is built and unit-tested only and has no native qualification evidence; "+
			"natively qualified hosts: %s",
		currentHost(),
		qualified,
	)
}

func (p Profile) SBX(version, revision string) error {
	for _, v := range p.QualifiedSBXVersions {
		if version == v && revision != "" && p.QualifiedSBXRevisions[version] == revision {
			return nil
		}
	}
	return Fail("E_SBX_VERSION", "sbx %q revision %q has no qualified profile", version, revision)
}

type semver struct {
	core [3]uint64
	pre  []string
}

func parseSemver(s string) (semver, error) {
	var out semver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if strings.Contains(s, "+") {
		return out, errors.New("build metadata is not accepted in a Devsy version")
	}
	number, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(number, ".")
	if len(parts) != 3 {
		return out, errors.New("expected one semantic version")
	}
	if err := parseCore(parts, &out); err != nil {
		return out, err
	}
	if !hasPre {
		return out, nil
	}
	out.pre = strings.Split(pre, ".")
	for _, id := range out.pre {
		if !validPrereleaseIdentifier(id) {
			return semver{}, errors.New("invalid semantic version prerelease")
		}
	}
	return out, nil
}

func parseCore(parts []string, out *semver) error {
	for i, v := range parts {
		if v == "" || (len(v) > 1 && v[0] == '0') {
			return errors.New("invalid semantic version")
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return err
		}
		out.core[i] = n
	}
	return nil
}

func validPrereleaseIdentifier(id string) bool {
	if id == "" {
		return false
	}
	numeric := true
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9':
		case validPrereleaseLetter(c):
			numeric = false
		default:
			return false
		}
	}
	return !numeric || validNumericIdentifier(id)
}

func validNumericIdentifier(id string) bool { return id == "0" || id[0] != '0' }

func validPrereleaseLetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-'
}

func compareSemver(a, b semver) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	if len(a.pre) == 0 || len(b.pre) == 0 {
		return compareRelease(a.pre, b.pre)
	}
	return comparePrereleases(a.pre, b.pre)
}

func comparePrereleases(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePrereleaseIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func compareRelease(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		switch {
		case len(a) == len(b):
			return 0
		case len(a) == 0:
			return 1
		default:
			return -1
		}
	}
	return 0
}

func comparePrereleaseIdentifier(a, b string) int {
	an, aErr := strconv.ParseUint(a, 10, 64)
	bn, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
		return 0
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func HostEnvironment() []string {
	inherited := os.Environ()
	result := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "COMMAND") ||
			strings.EqualFold(key, "DEVSY_PROVIDER_CANCEL_FILE") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (p Profile) Devsy(ctx context.Context, path string) error {
	if p.MinimumDevsyVersion == "" {
		return Fail(
			"E_DEVSY_VERSION",
			"no published Devsy release containing required core changes is qualified",
		)
	}
	minimum, err := parseSemver(p.MinimumDevsyVersion)
	if err != nil {
		return Wrap("E_DEVSY_VERSION", err)
	}
	if path == "" {
		return Fail("E_DEVSY_VERSION", "DEVSY is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Env = HostEnvironment()
	out, err := command.Output()
	if err != nil {
		return Wrap("E_DEVSY_VERSION", err)
	}
	version, err := parseSemver(string(out))
	if err != nil {
		return Wrap("E_DEVSY_VERSION", err)
	}
	if version.core == [3]uint64{} {
		return Fail("E_DEVSY_VERSION", "development Devsy v0.0.0 is not a qualified release")
	}
	if compareSemver(version, minimum) < 0 {
		return Fail(
			"E_DEVSY_VERSION",
			"Devsy %s is older than required %s",
			strings.TrimSpace(string(out)),
			p.MinimumDevsyVersion,
		)
	}
	return nil
}

func (p Profile) RequireDaemonSchema() error {
	if p.DaemonSchema == "" || len(p.QualifiedDaemonVersions) == 0 {
		return Fail(
			"E_SBX_VERSION",
			"successful-server daemon version and client/daemon tuple have not been qualified",
		)
	}
	return nil
}

func (p Profile) RequireInventorySchema() error {
	if len(p.InventoryStates) == 0 {
		return Fail(
			"E_SBX_SCHEMA",
			"successful-server inventory state schema has not been qualified",
		)
	}
	return nil
}

func (p Profile) RequireShellSchema() error {
	if p.ShellSettingSchema != ShellSettingSchemaV0501 {
		return Fail("E_SHELL_OVERRIDE", "effective shell shortcut settings have not been qualified")
	}
	return nil
}

func (p Profile) RequireOperationResultSchema() error {
	if p.OperationResultSchema != OperationResultSchemaV0501 {
		return Fail(
			"E_SBX_SCHEMA",
			"successful sbx stop/remove JSON has no qualified result schema",
		)
	}
	return nil
}

func MeasuredDiskSizingSchema(host, version, revision string) string {
	if host == "darwin/arm64" && version == "v0.50.1" &&
		revision == "ddb0268d7124b9cf98955d0b08ffd71a0dfcebe4" {
		return DiskSizingSchemaDarwinARM64V0501
	}
	return ""
}

func (p Profile) RequireDiskSizingQualification() error {
	if !p.HostQualified() {
		return nil
	}
	host := currentHost()
	if len(p.QualifiedSBXVersions) != 1 {
		return Fail(
			"E_GUEST_CAPABILITY",
			"root and Docker filesystem capacity overhead is not qualified for this host",
		)
	}
	version := p.QualifiedSBXVersions[0]
	expected := MeasuredDiskSizingSchema(host, version, p.QualifiedSBXRevisions[version])
	if expected == "" || p.DiskSizingSchemas[host] != expected {
		return Fail(
			"E_GUEST_CAPABILITY",
			"root and Docker filesystem capacity overhead is not qualified for this host",
		)
	}
	return nil
}

func ConfigRoot() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", Wrap("E_STORAGE", err)
	}
	return base, nil
}
