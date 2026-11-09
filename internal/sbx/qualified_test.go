package sbx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

const testRemoved = "removed"

func qualifiedFixture(t *testing.T, p compat.Profile, name string) []byte {
	t.Helper()
	root, err := os.OpenRoot("testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	data, err := root.ReadFile(filepath.Join(p.QualifiedSBXVersions[0], name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestQualifiedVersionAndSettingsCaptures(t *testing.T) {
	p, err := compat.Load()
	if err != nil {
		t.Fatal(err)
	}
	var version Version
	if err := exact(qualifiedFixture(t, p, "version-daemon-running.json"), &version); err != nil {
		t.Fatal(err)
	}
	daemon, err := DaemonVersion(version, p)
	if err != nil || !p.TupleQualified(compat.Tuple{
		Client: version.Client.Version, Revision: version.Client.Revision, Daemon: daemon,
	}) {
		t.Fatalf("captured client and daemon are not qualified: %+v, %v", version, err)
	}
	if err := checkShellSetting(
		qualifiedFixture(t, p, "settings-shell-full.json"),
		false,
	); err != nil {
		t.Fatal(err)
	}
}

func TestQualifiedLifecycleCaptures(t *testing.T) {
	p, err := compat.Load()
	if err != nil {
		t.Fatal(err)
	}
	owned := qualifiedInventory(t, p, "running")
	inspection, err := decodeInspection(
		qualifiedFixture(t, p, "inspect-mountless.json"),
		p,
		owned.Name,
		false,
	)
	if err != nil || inspection.State != owned.State || len(inspection.RuntimeMounts) != 0 {
		t.Fatalf("captured mountless inspection: %+v, %v", inspection, err)
	}
	stopped := qualifiedInventory(t, p, "stopped")
	if stopped.ID != owned.ID {
		t.Fatal("captured stop changed the immutable sandbox ID")
	}
}

func qualifiedInventory(t *testing.T, p compat.Profile, state string) Sandbox {
	t.Helper()
	items, err := decodeInventory(qualifiedFixture(t, p, "inventory-"+state+".json"))
	if err != nil || len(items) != 1 || !items[0].Detached || items[0].State != state {
		t.Fatalf("captured inventory: %+v, %v", items, err)
	}
	return items[0]
}

func TestQualifiedOperationResultCaptures(t *testing.T) {
	p, err := compat.Load()
	if err != nil {
		t.Fatal(err)
	}
	owned := qualifiedInventory(t, p, "running")
	for _, result := range []struct{ file, action string }{
		{"stop-result.json", "stopped"}, {"rm-result.json", testRemoved},
	} {
		if err := CheckResult(
			qualifiedFixture(t, p, result.file),
			owned.Name,
			result.action,
			false,
		); err != nil {
			t.Fatal(err)
		}
	}
	absent, err := decodeInventory(qualifiedFixture(t, p, "inventory-after-remove.json"))
	if err != nil || len(absent) != 0 {
		t.Fatalf("captured removal inventory: %+v, %v", absent, err)
	}
}
