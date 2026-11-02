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
