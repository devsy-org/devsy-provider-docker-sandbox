package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

type Spec struct {
	CPUs            int    `json:"cpus"`
	MemoryBytes     int64  `json:"memory_bytes"`
	RootDiskBytes   int64  `json:"root_disk_bytes"`
	DockerDiskBytes int64  `json:"docker_disk_bytes"`
	WorkspaceMode   string `json:"workspace_mode"`
	Agent           string `json:"agent"`
	Skills          string `json:"skills"`
}
type Config struct {
	SBXPath string
	Devsy   string
	Spec    Spec
	Getenv  func(string) string
}
type MachineIdentity struct {
	Key             string
	Folder          string
	ID              string
	Context         string
	Provider        string
	ProviderID      string
	ProviderContext string
	ProviderFolder  string
}

const Family = "devsy-org/devsy-provider-docker-sandbox"

func option(getenv func(string) string, k, def string) string {
	if v := getenv(k); v != "" {
		return v
	}
	return def
}

func size(s string, minimum int64) (int64, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	unit := s[len(s)-1]
	multiplier := int64(1 << 20)
	if unit == 'g' {
		multiplier = 1 << 30
	} else if unit != 'm' {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n > int64(^uint64(0)>>1)/multiplier {
		return 0, errors.New("size overflow")
	}
	bytes := n * multiplier
	if bytes < minimum {
		return 0, fmt.Errorf("size %q is below the minimum of %s", s, formatSize(minimum))
	}
	return bytes, nil
}

func formatSize(bytes int64) string {
	if bytes%(1<<30) == 0 {
		return strconv.FormatInt(bytes>>30, 10) + "g"
	}
	return strconv.FormatInt(bytes>>20, 10) + "m"
}

func invalidExecutablePath(path string) bool {
	return strings.TrimSpace(path) != path ||
		strings.IndexFunc(path, func(r rune) bool { return r < 32 }) >= 0 ||
		(!filepath.IsAbs(path) && (strings.ContainsRune(path, '/') || strings.ContainsRune(path, 92)))
}

func executableFile(info os.FileInfo, err error) bool {
	return err == nil && info.Mode().IsRegular() &&
		(runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0)
}

func executable(getenv func(string) string) (string, error) {
	path := option(getenv, "SBX_PATH", "sbx")
	if invalidExecutablePath(path) {
		return "", compat.Fail(
			"E_SBX_NOT_INSTALLED",
			"SBX_PATH must be a bare executable or absolute executable path",
		)
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", compat.Fail(
			"E_SBX_NOT_INSTALLED",
			"sbx executable %q unavailable; install Docker Sandboxes from https://docs.docker.com/ai/sandboxes/get-started/",
			path,
		)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", compat.Wrap("E_SBX_NOT_INSTALLED", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", compat.Wrap("E_SBX_NOT_INSTALLED", err)
	}
	info, err := os.Stat(resolved)
	if !executableFile(info, err) {
		return "", compat.Fail(
			"E_SBX_NOT_INSTALLED",
			"SBX_PATH %q is not a regular executable",
			path,
		)
	}
	return resolved, nil
}

func Load(getenv func(string) string) (Config, error) {
	return load(getenv, true)
}

func LoadExisting(getenv func(string) string) (Config, error) {
	return load(getenv, false)
}

func load(getenv func(string) string, enforceMinimums bool) (Config, error) {
	c := Config{Getenv: getenv, Devsy: getenv("DEVSY")}
	path, err := executable(getenv)
	if err != nil {
		return c, err
	}
	c.SBXPath = path
	cpus, err := strconv.Atoi(option(getenv, "SBX_CPUS", "2"))
	if err != nil || cpus < 1 || cpus > runtime.NumCPU() {
		return c, compat.Fail(
			"E_CONFIG_DRIFT",
			"SBX_CPUS must be between 1 and %d",
			runtime.NumCPU(),
		)
	}
	c.Spec.CPUs = cpus
	for _, entry := range []struct {
		key          string
		min          int64
		dest         *int64
		defaultValue string
	}{
		{"SBX_MEMORY", 512 << 20, &c.Spec.MemoryBytes, "4g"},
		{"SBX_ROOT_DISK", 8 << 30, &c.Spec.RootDiskBytes, "40g"},
		{"SBX_DOCKER_DISK", 7 << 30, &c.Spec.DockerDiskBytes, "40g"},
	} {
		minimum := entry.min
		if !enforceMinimums {
			minimum = 0
		}
		v, e := size(option(getenv, entry.key, entry.defaultValue), minimum)
		if e != nil {
			return c, compat.Fail("E_CONFIG_DRIFT", "%s: %v", entry.key, e)
		}
		*entry.dest = v
	}
	c.Spec.WorkspaceMode = "mountless"
	c.Spec.Agent = "shell"
	c.Spec.Skills = "off"
	return c, nil
}

func LoadScoped(getenv func(string) string) (Config, error) {
	path, e := executable(getenv)
	if e != nil {
		return Config{}, e
	}
	return Config{SBXPath: path, Devsy: getenv("DEVSY"), Getenv: getenv}, nil
}

func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	var suffix []string
	cur := abs
	for {
		resolved, e := filepath.EvalSymlinks(cur)
		if e == nil {
			for _, part := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, part)
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", e
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func Key(folder string) (string, error) {
	path, err := canonical(folder)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Family        string `json:"family"`
		MachineFolder string `json:"machineFolder"`
	}{Family, path})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (c Config) RequireMachine() (MachineIdentity, error) {
	g := c.Getenv
	m := MachineIdentity{
		ID:              g("MACHINE_ID"),
		Context:         g("MACHINE_CONTEXT"),
		Provider:        g("MACHINE_PROVIDER"),
		ProviderID:      g("PROVIDER_ID"),
		ProviderContext: g("PROVIDER_CONTEXT"),
		ProviderFolder:  g("PROVIDER_FOLDER"),
	}
	folder := g("MACHINE_FOLDER")
	if !m.complete(folder) {
		return m, compat.Fail(
			"E_STATE_MISSING",
			"required MACHINE_* or PROVIDER_* identity unavailable",
		)
	}
	var err error
	m.Folder, err = canonical(folder)
	if err != nil {
		return m, compat.Wrap("E_STATE_MISSING", err)
	}
	m.Key, err = Key(m.Folder)
	if err != nil {
		return m, compat.Wrap("E_STATE_MISSING", err)
	}
	return m, nil
}

func (m MachineIdentity) complete(folder string) bool {
	return !slices.Contains([]string{
		folder, m.ID, m.Context, m.Provider, m.ProviderID, m.ProviderContext, m.ProviderFolder,
	}, "")
}
