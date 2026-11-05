package state

import (
	"context"
	"encoding/json"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableJournalAndAttempts(t *testing.T) {
	folder := t.TempDir()
	key, e := config.Key(folder)
	if e != nil {
		t.Fatal(e)
	}
	s := Store{Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"), Key: key}
	r, e := New(config.MachineIdentity{Key: key, Folder: folder, ID: "machine", Context: "context"}, config.Spec{CPUs: 2})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Save(r); e != nil {
		t.Fatal(e)
	}
	loaded, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if loaded.SandboxName != r.SandboxName || loaded.Generation != r.Generation {
		t.Fatal("ownership changed after journal reload")
	}
	id, e := Token()
	if e != nil {
		t.Fatal(e)
	}
	a := Attempt{SchemaVersion: 1, AttemptID: id, MachineKey: key, Generation: r.Generation, Kind: "command", Phase: "admitted"}
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
	data, e := os.ReadFile(filepath.Join(s.Dir(), "machine.json"))
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
}
func TestAttemptsIgnoreInterruptedAtomicSave(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"), Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	a := Attempt{SchemaVersion: 1, AttemptID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", MachineKey: s.Key, Generation: "cccccccccccccccccccccccccccccccc", Phase: "admitted"}
	if err := s.SaveAttempt(a); err != nil {
		t.Fatal(err)
	}
	temp, err := os.CreateTemp(filepath.Join(s.Dir(), "attempts"), ".atomic-")
	if err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	attempts, err := s.Attempts()
	if err != nil || len(attempts) != 1 || attempts[0].AttemptID != a.AttemptID {
		t.Fatalf("interrupted save changed visible attempts: %v, %v", attempts, err)
	}
	incomplete, err := s.Incomplete()
	if err != nil || len(incomplete) != 1 || incomplete[0].AttemptID != a.AttemptID {
		t.Fatalf("interrupted save changed incomplete attempts: %v, %v", incomplete, err)
	}
}

func TestAttemptsRejectUnsafeEntries(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		make  func(string) error
	}{
		{"unexpected file", "other", func(path string) error { return os.WriteFile(path, nil, 0600) }},
		{"empty temp suffix", ".atomic-", func(path string) error { return os.WriteFile(path, nil, 0600) }},
		{"temp masquerading as journal", ".atomic-fake.json", func(path string) error { return os.WriteFile(path, nil, 0600) }},
		{"temp directory", ".atomic-fake", func(path string) error { return os.Mkdir(path, 0700) }},
		{"public temp", ".atomic-public", func(path string) error {
			if err := os.WriteFile(path, nil, 0600); err != nil {
				return err
			}
			return os.Chmod(path, 0644)
		}},
		{"temp symlink", ".atomic-link", func(path string) error {
			return os.Symlink(filepath.Join(filepath.Dir(path), "missing"), path)
		}},
		{"journal symlink", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.json", func(path string) error {
			return os.Symlink(filepath.Join(filepath.Dir(path), "missing"), path)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if os.PathSeparator == 92 && (tc.name == "temp symlink" || tc.name == "journal symlink" || tc.name == "public temp") {
				t.Skip("symlink privileges and mode checks differ on Windows")
			}
			s := Store{Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"), Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
			if err := s.Ensure(); err != nil {
				t.Fatal(err)
			}
			if err := tc.make(filepath.Join(s.Dir(), "attempts", tc.entry)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Attempts(); err == nil {
				t.Fatal("unsafe attempt entry accepted")
			}
			if _, err := s.Incomplete(); err == nil {
				t.Fatal("unsafe attempt entry bypassed incomplete scan")
			}
		})
	}
}

func TestCrossProcessLockCancellation(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"), Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	lease, e := s.Acquire(context.Background(), "mutation", false)
	if e != nil {
		t.Fatal(e)
	}
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
	s := Store{Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"), Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if e := s.Ensure(); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "target")
	if e := os.WriteFile(target, []byte("{}"), 0600); e != nil {
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
	s := Store{Root: filepath.Join(alias, "devsy-provider-docker-sandbox", "state-v1"), Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
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
	s := Store{Root: filepath.Join(owner, "state-v1"), Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := s.Ensure(); err == nil {
		t.Fatal("provider-owned state symlink accepted")
	}
}
