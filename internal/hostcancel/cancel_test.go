package hostcancel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func privateMarker(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "invocation")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "cancel")
}

func TestPreexistingMarker(t *testing.T) {
	path := privateMarker(t)
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, join, e := Watch(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := join(); err != nil {
			t.Error(err)
		}
	})
	if ctx.Err() != context.Canceled {
		t.Fatalf("existing marker not immediately observed: %v", ctx.Err())
	}
}
func TestMarkerAppearingAndWatcherJoin(t *testing.T) {
	path := privateMarker(t)
	ctx, join, e := Watch(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("marker not observed")
	}
	if e = join(); e != nil {
		t.Fatal(e)
	}
}
func TestMissingDirectoryFailsClosed(t *testing.T) {
	_, _, e := Watch(context.Background(), filepath.Join(t.TempDir(), "gone", "cancel"))
	if e == nil {
		t.Fatal("missing marker directory accepted")
	}
}
func TestSymlinkMarkerFailsClosed(t *testing.T) {
	if os.PathSeparator == 92 {
		t.Skip("symlink privileges vary on Windows")
	}
	marker := privateMarker(t)
	dir := filepath.Dir(marker)
	target := filepath.Join(dir, "target")
	if e := os.WriteFile(target, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, marker); e != nil {
		t.Fatal(e)
	}
	if _, _, e := Watch(context.Background(), marker); e == nil {
		t.Fatal("symlink marker accepted")
	}
}
func TestSymlinkInvocationDirectoryFailsClosed(t *testing.T) {
	if os.PathSeparator == 92 {
		t.Skip("symlink privileges vary on Windows")
	}
	real := filepath.Dir(privateMarker(t))
	alias := filepath.Join(t.TempDir(), "invocation-alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Watch(context.Background(), filepath.Join(alias, "cancel")); err == nil {
		t.Fatal("symlink invocation directory accepted")
	}
}
