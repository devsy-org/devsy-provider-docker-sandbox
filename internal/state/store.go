package state

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Operation struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Phase         string `json:"phase"`
	Step          string `json:"step"`
	LastAttemptID string `json:"last_attempt_id"`
}
type Compatibility struct {
	SBXVersion       string `json:"sbx_version"`
	SBXDaemonVersion string `json:"sbx_daemon_version"`
	SBXRevision      string `json:"sbx_revision"`
	TemplateIdentity string `json:"template_identity"`
}
type Record struct {
	SchemaVersion  int           `json:"schema_version"`
	MachineKey     string        `json:"machine_key"`
	MachineFolder  string        `json:"machine_folder"`
	MachineID      string        `json:"machine_id"`
	Context        string        `json:"context"`
	ProviderFamily string        `json:"provider_family"`
	ProviderAlias  string        `json:"provider_alias"`
	SandboxName    string        `json:"sandbox_name"`
	SandboxID      string        `json:"sandbox_id"`
	Generation     string        `json:"generation"`
	Epoch          uint64        `json:"epoch"`
	Phase          string        `json:"phase"`
	DesiredState   string        `json:"desired_state"`
	Operation      *Operation    `json:"operation"`
	Spec           config.Spec   `json:"spec"`
	Compatibility  Compatibility `json:"compatibility"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	LastErrorCode  string        `json:"last_error_code"`
}
type Attempt struct {
	SchemaVersion           int        `json:"schema_version"`
	AttemptID               string     `json:"attempt_id"`
	OperationID             string     `json:"operation_id"`
	MachineKey              string     `json:"machine_key"`
	SandboxName             string     `json:"sandbox_name"`
	Generation              string     `json:"generation"`
	Epoch                   uint64     `json:"epoch"`
	Kind                    string     `json:"kind"`
	Phase                   string     `json:"phase"`
	StartedAt               *time.Time `json:"started_at"`
	CompletedAt             *time.Time `json:"completed_at"`
	DiagnosticPID           *int       `json:"diagnostic_pid"`
	DiagnosticStartIdentity string     `json:"diagnostic_start_identity"`
	ExitCode                *int       `json:"exit_code"`
	Outcome                 string     `json:"outcome"`
}
type Store struct {
	Root string
	Key  string
}

func nowUTC() time.Time { return time.Now().UTC() }
func compatRoot() (string, error) {
	base, err := compat.ConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "devsy-provider-docker-sandbox", "state-v1"), nil
}
func Open(key string) (Store, error) {
	if len(key) != 64 {
		return Store{}, compat.Fail("E_STATE_MISSING", "machine key must contain 64 hex digits")
	}
	if _, err := hex.DecodeString(key); err != nil || strings.ToLower(key) != key {
		return Store{}, compat.Fail("E_STATE_MISSING", "invalid machine key")
	}
	root, err := compatRoot()
	if err != nil {
		return Store{}, err
	}
	return Store{Root: root, Key: key}, nil
}
func validToken(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	_, e := hex.DecodeString(value)
	return e == nil
}
func (s Store) Dir() string { return filepath.Join(s.Root, s.Key) }
func ownedStatePath(path string) bool {
	return filepath.Base(path) == "devsy-provider-docker-sandbox" || strings.Contains(path, string(os.PathSeparator)+"devsy-provider-docker-sandbox"+string(os.PathSeparator))
}
func privateDir(path string) error {
	fi, err := os.Lstat(path)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if ownedStatePath(path) {
			return fmt.Errorf("provider state directory is a symlink: %s", path)
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return resolveErr
		}
		return privateDir(resolved)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent != path {
		if err := privateDir(parent); err != nil {
			return err
		}
	}
	if os.IsNotExist(err) {
		if parent == path {
			return err
		}
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	fi, err = os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state directory is not a real directory: %s", path)
	}
	if ownedStatePath(path) {
		if err := checkDir(fi); err != nil {
			return err
		}
		return protectDir(path)
	}
	return nil
}
func (s Store) Ensure() error {
	if err := privateDir(s.Dir()); err != nil {
		return compat.Wrap("E_STORAGE", err)
	}
	return compat.Wrap("E_STORAGE", privateDir(filepath.Join(s.Dir(), "attempts")))
}
func readFile(path string) ([]byte, error) {
	fi, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 || fi.Size() > 16<<20 {
		return nil, compat.Fail("E_STORAGE", "unsafe or oversized state file %s", path)
	}
	if e := checkFile(fi); e != nil {
		return nil, compat.Wrap("E_STORAGE", e)
	}
	return os.ReadFile(path)
}
func (s Store) Load() (Record, error) {
	var r Record
	data, e := readFile(filepath.Join(s.Dir(), "machine.json"))
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(data, &r); e != nil {
		return r, compat.Wrap("E_STORAGE", e)
	}
	if r.SchemaVersion != 1 || r.MachineKey != s.Key || !validToken(r.Generation) || r.SandboxName != Name(s.Key, r.Generation) || r.ProviderFamily != config.Family {
		return r, compat.Fail("E_STORAGE", "invalid machine record or ownership")
	}
	key, e := config.Key(r.MachineFolder)
	if e != nil || key != s.Key {
		return r, compat.Fail("E_OWNERSHIP_MISMATCH", "journal machine path no longer matches canonical ownership")
	}
	return r, nil
}
func save(path string, v any) (result error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = privateDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".atomic-")
	if err != nil {
		return err
	}
	tempPath := f.Name()
	defer func() {
		if cleanupErr := os.Remove(tempPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			result = errors.Join(result, cleanupErr)
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = replaceFile(tempPath, path); err != nil {
		return err
	}
	return syncDir(dir)
}
func (s Store) Save(r Record) error {
	if r.MachineKey != s.Key || r.SchemaVersion != 1 || !validToken(r.Generation) || r.SandboxName != Name(s.Key, r.Generation) || r.ProviderFamily != config.Family {
		return compat.Fail("E_STORAGE", "invalid journal ownership")
	}
	r.UpdatedAt = nowUTC()
	return compat.Wrap("E_STORAGE", save(filepath.Join(s.Dir(), "machine.json"), r))
}
func (s Store) SaveAttempt(a Attempt) error {
	if a.MachineKey != s.Key || a.SchemaVersion != 1 || !validToken(a.AttemptID) || !validToken(a.Generation) {
		return compat.Fail("E_STORAGE", "invalid attempt identity")
	}
	return compat.Wrap("E_STORAGE", save(filepath.Join(s.Dir(), "attempts", a.AttemptID+".json"), a))
}
func (s Store) Attempts() ([]Attempt, error) {
	entries, e := os.ReadDir(filepath.Join(s.Dir(), "attempts"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, compat.Wrap("E_STORAGE", e)
	}
	out := make([]Attempt, 0, len(entries))
	for _, v := range entries {
		if strings.HasPrefix(v.Name(), ".atomic-") && len(v.Name()) > len(".atomic-") && !strings.HasSuffix(v.Name(), ".json") {
			fi, e := v.Info()
			if e != nil {
				return nil, compat.Wrap("E_STORAGE", e)
			}
			if !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 || fi.Size() > 16<<20 {
				return nil, compat.Fail("E_STORAGE", "unsafe attempt temp file")
			}
			if e := checkFile(fi); e != nil {
				return nil, compat.Wrap("E_STORAGE", e)
			}
			continue
		}
		if !strings.HasSuffix(v.Name(), ".json") {
			return nil, compat.Fail("E_STORAGE", "unexpected attempt file")
		}
		data, e := readFile(filepath.Join(s.Dir(), "attempts", v.Name()))
		if e != nil {
			return nil, compat.Wrap("E_STORAGE", e)
		}
		var a Attempt
		if e = json.Unmarshal(data, &a); e != nil {
			return nil, compat.Wrap("E_STORAGE", e)
		}
		if a.SchemaVersion != 1 || a.MachineKey != s.Key || !validToken(a.Generation) || !validToken(a.AttemptID) || a.AttemptID+".json" != v.Name() {
			return nil, compat.Fail("E_STORAGE", "invalid attempt record")
		}
		out = append(out, a)
	}
	return out, nil
}
func (s Store) Incomplete() ([]Attempt, error) {
	all, e := s.Attempts()
	if e != nil {
		return nil, e
	}
	out := make([]Attempt, 0)
	for _, a := range all {
		if a.Phase != "client_reaped" && a.Phase != "reconciled" {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s Store) Archive(r Record) error {
	if !validToken(r.Generation) || r.MachineKey != s.Key {
		return compat.Fail("E_STORAGE", "invalid archived generation")
	}
	return compat.Wrap("E_STORAGE", save(filepath.Join(s.Dir(), "generations", r.Generation+".json"), r))
}
func (s Store) ControlCWD() (string, func() error, error) {
	dir := filepath.Join(s.Root, "control-cwd")
	if err := privateDir(dir); err != nil {
		return "", nil, compat.Wrap("E_STORAGE", err)
	}
	path, err := os.MkdirTemp(dir, "call-")
	if err != nil {
		return "", nil, compat.Wrap("E_STORAGE", err)
	}
	return path, func() error { return os.Remove(path) }, nil
}
func (s Store) ListKeys() ([]string, error) {
	entries, e := os.ReadDir(s.Root)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, compat.Wrap("E_STORAGE", e)
	}
	keys := []string{}
	for _, v := range entries {
		if len(v.Name()) != 64 || !v.IsDir() {
			continue
		}
		if _, e := hex.DecodeString(v.Name()); e == nil {
			keys = append(keys, v.Name())
		}
	}
	return keys, nil
}
func (s Store) ReadAttempt(id string) (Attempt, error) {
	var a Attempt
	if !validToken(id) {
		return a, compat.Fail("E_UNCLEAN_ATTEMPT", "invalid attempt ID")
	}
	data, e := readFile(filepath.Join(s.Dir(), "attempts", id+".json"))
	if e != nil {
		return a, compat.Wrap("E_STORAGE", e)
	}
	if e = json.Unmarshal(data, &a); e != nil {
		return a, compat.Wrap("E_STORAGE", e)
	}
	if a.MachineKey != s.Key || a.AttemptID != id || a.SchemaVersion != 1 || !validToken(a.Generation) {
		return a, compat.Fail("E_STORAGE", "invalid attempt record")
	}
	return a, nil
}
