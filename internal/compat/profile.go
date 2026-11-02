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
	"strconv"
	"strings"
	"time"
)

//go:embed profile.json
var profileFS embed.FS

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
}

type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string                    { return e.Code + ": " + e.Detail }
func Fail(code, format string, args ...any) error { return &Error{code, fmt.Sprintf(format, args...)} }
func Wrap(code string, err error) error {
	if err == nil {
		return nil
	}
	var tagged *Error
	if errors.As(err, &tagged) {
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
func (p Profile) Host() error {
	tuple := runtime.GOOS + "/" + runtime.GOARCH
	ok := false
	for _, v := range p.QualifiedHosts {
		if v == tuple {
			ok = true
		}
	}
	if !ok {
		return Fail("E_HOST_UNSUPPORTED", "%s has no native-qualified profile; qualified hosts: %v", tuple, p.QualifiedHosts)
	}
	return hostVersion()
}
func (p Profile) SBX(version, revision string) error {
	for _, v := range p.QualifiedSBXVersions {
		if version == v && revision != "" && p.QualifiedSBXRevisions[version] == revision {
			return nil
		}
	}
	return Fail("E_SBX_VERSION", "sbx %q revision %q has no qualified profile", version, revision)
}
func parseSemver(s string) ([3]uint64, error) {
	var out [3]uint64
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("expected one semantic version")
	}
	for i, v := range parts {
		if v == "" || (len(v) > 1 && v[0] == '0') {
			return out, fmt.Errorf("invalid semantic version")
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}
func HostEnvironment() []string {
	inherited := os.Environ()
	result := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "COMMAND") || strings.EqualFold(key, "DEVSY_PROVIDER_CANCEL_FILE") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
func (p Profile) Devsy(ctx context.Context, path string) error {
	if p.MinimumDevsyVersion == "" {
		return Fail("E_DEVSY_VERSION", "no published Devsy release containing required core changes is qualified")
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
	if version == [3]uint64{} {
		return Fail("E_DEVSY_VERSION", "development Devsy v0.0.0 is not a qualified release")
	}
	for i := range version {
		if version[i] < minimum[i] {
			return Fail("E_DEVSY_VERSION", "Devsy %s is older than required %s", strings.TrimSpace(string(out)), p.MinimumDevsyVersion)
		}
		if version[i] > minimum[i] {
			break
		}
	}
	return nil
}
func (p Profile) RequireDaemonSchema() error {
	if p.DaemonSchema == "" || len(p.QualifiedDaemonVersions) == 0 {
		return Fail("E_SBX_VERSION", "successful-server daemon version and client/daemon tuple have not been qualified")
	}
	return nil
}
func (p Profile) RequireInventorySchema() error {
	if len(p.InventoryStates) == 0 {
		return Fail("E_SBX_SCHEMA", "successful-server inventory state schema has not been qualified")
	}
	return nil
}
func (p Profile) RequireShellSchema() error {
	if p.ShellSettingSchema == "" {
		return Fail("E_SHELL_OVERRIDE", "effective shell shortcut settings have not been qualified")
	}
	return nil
}
func (p Profile) RequireOperationResultSchema() error {
	return Fail("E_SBX_SCHEMA", "successful sbx stop/remove JSON has no captured v0.50.1 fixture; exact target result decoding is blocked")
}
func (p Profile) RequireDiskSizingQualification() error {
	return Fail("E_GUEST_CAPABILITY", "root and Docker filesystem capacity overhead have not been measured for the qualified host; provisioning is blocked")
}
func ConfigRoot() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", Wrap("E_STORAGE", err)
	}
	return base, nil
}
