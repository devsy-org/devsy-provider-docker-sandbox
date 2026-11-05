package state

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
)

func TestDurableJournalAndAttempts(t *testing.T) {
	folder := t.TempDir()
	key, e := config.Key(folder)
	requireNoError(t, e)
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  key,
	}
	r, e := New(
		config.MachineIdentity{Key: key, Folder: folder, ID: "machine", Context: "context"},
		config.Spec{CPUs: 2},
	)
	requireNoError(t, e)
	if e = s.Save(r); e != nil {
		t.Fatal(e)
	}
	loaded, e := s.Load()
	requireNoError(t, e)
	if loaded.SandboxName != r.SandboxName || loaded.Generation != r.Generation {
		t.Fatal("ownership changed after journal reload")
	}
	checkDurableAttempts(t, s, r, key)
	data, e := os.ReadFile(filepath.Join(s.Dir(), "machine.json"))
	requireNoError(t, e)
	var doc map[string]any
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
}

func checkDurableAttempts(t *testing.T, s Store, r Record, key string) {
	id, e := Token()
	requireNoError(t, e)
	a := Attempt{
		SchemaVersion: 1,
		AttemptID:     id,
		MachineKey:    key,
		Generation:    r.Generation,
		Kind:          "command",
		Phase:         "admitted",
	}
	if e = s.SaveAttempt(a); e != nil {
		t.Fatal(e)
	}
	incomplete, e := s.Incomplete()
	if e != nil || len(incomplete) != 1 {
		t.Fatalf("unsettled attempt missing: %v", e)
	}
	a.Phase = "client_reaped"
	if e = s.SaveAttempt(a); e != nil {
		t.Fatal(e)
	}
	incomplete, e = s.Incomplete()
	if e != nil || len(incomplete) != 0 {
		t.Fatalf("completed attempt remains unsettled: %v", e)
	}
}

func TestAttemptsIgnoreInterruptedAtomicSave(t *testing.T) {
	s, a := interruptedAttempts(t)
	attempts, err := s.Attempts()
	if err != nil || len(attempts) != 1 || attempts[0].AttemptID != a.AttemptID {
		t.Fatalf("interrupted save changed visible attempts: %v, %v", attempts, err)
	}
	incomplete, err := s.Incomplete()
	if err != nil || len(incomplete) != 1 || incomplete[0].AttemptID != a.AttemptID {
		t.Fatalf("interrupted save changed incomplete attempts: %v, %v", incomplete, err)
	}
}

func interruptedAttempts(t *testing.T) (Store, Attempt) {
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	a := Attempt{
		SchemaVersion: 1,
		AttemptID:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MachineKey:    s.Key,
		Generation:    "cccccccccccccccccccccccccccccccc",
		Phase:         "admitted",
	}
	if err := s.SaveAttempt(a); err != nil {
		t.Fatal(err)
	}
	temp, err := os.CreateTemp(filepath.Join(s.Dir(), "attempts"), ".atomic-")
	requireNoError(t, err)
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}

	return s, a
}

func TestAttemptsRejectUnsafeEntries(t *testing.T) {
	for _, tc := range unsafeAttemptCases() {
		t.Run(tc.name, func(t *testing.T) { checkUnsafeAttemptEntry(t, tc) })
	}
}

type unsafeAttemptCase struct {
	name, entry string
	make        func(string) error
}

func unsafeAttemptCases() []unsafeAttemptCase {
	return []unsafeAttemptCase{
		{
			"unexpected file",
			"other",
			func(path string) error { return os.WriteFile(path, nil, 0o600) },
		},
		{
			"empty temp suffix",
			".atomic-",
			func(path string) error { return os.WriteFile(path, nil, 0o600) },
		},
		{
			"temp masquerading as journal",
			".atomic-fake.json",
			func(path string) error { return os.WriteFile(path, nil, 0o600) },
		},
		{
			"temp directory",
			".atomic-fake",
			func(path string) error { return os.Mkdir(path, 0o700) },
		},
		{"public temp", ".atomic-public", func(path string) error {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
			root, err := os.OpenRoot(filepath.Dir(path))
			if err != nil {
				return err
			}
			defer func() { _ = root.Close() }()
			return root.Chmod(filepath.Base(path), 0o644)
		}},
		{"temp symlink", ".atomic-link", func(path string) error {
			return os.Symlink(filepath.Join(filepath.Dir(path), "missing"), path)
		}},
		{"journal symlink", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.json", func(path string) error {
			return os.Symlink(filepath.Join(filepath.Dir(path), "missing"), path)
		}},
	}
}

func checkUnsafeAttemptEntry(t *testing.T, tc unsafeAttemptCase) {
	if unsupportedUnsafeEntry(tc.name) {
		t.Skip("symlink privileges and mode checks differ on Windows")
	}
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	requireNoError(t, s.Ensure())
	requireNoError(t, tc.make(filepath.Join(s.Dir(), "attempts", tc.entry)))
	if _, err := s.Attempts(); err == nil {
		t.Fatal("unsafe attempt entry accepted")
	}
	if _, err := s.Incomplete(); err == nil {
		t.Fatal("unsafe attempt entry bypassed incomplete scan")
	}
}

func unsupportedUnsafeEntry(name string) bool {
	return os.PathSeparator == 92 &&
		(name == "temp symlink" || name == "journal symlink" || name == "public temp")
}

func TestCrossProcessLockCancellation(t *testing.T) {
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	lease, e := s.Acquire(context.Background(), "mutation", false)
	requireNoError(t, e)
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, e = s.Acquire(ctx, "mutation", false)
	if e == nil {
		t.Fatal("overlapping exclusive lease admitted")
	}
}

func TestRejectSymlinkJournal(t *testing.T) {
	if os.PathSeparator == 92 {
		t.Skip("symlink privileges vary on Windows")
	}
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	if e := s.Ensure(); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "target")
	if e := os.WriteFile(target, []byte("{}"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, filepath.Join(s.Dir(), "machine.json")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Load(); e == nil {
		t.Fatal("unsafe journal accepted")
	}
}

func TestCanonicalExternalAncestorSymlink(t *testing.T) {
	if os.PathSeparator == 92 {
		t.Skip("symlink privileges vary on Windows")
	}
	alias := filepath.Join(t.TempDir(), "external-alias")
	if err := os.Symlink(t.TempDir(), alias); err != nil {
		t.Fatal(err)
	}
	s := Store{
		Root: filepath.Join(alias, "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderOwnedDirectorySymlinkRejected(t *testing.T) {
	if os.PathSeparator == 92 {
		t.Skip("symlink privileges vary on Windows")
	}
	owner := filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox")
	if err := os.Symlink(t.TempDir(), owner); err != nil {
		t.Fatal(err)
	}
	s := Store{
		Root: filepath.Join(owner, "state-v1"),
		Key:  testMachineKey,
	}
	if err := s.Ensure(); err == nil {
		t.Fatal("provider-owned state symlink accepted")
	}
}
