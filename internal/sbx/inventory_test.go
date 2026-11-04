package sbx

import (
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"os"
	"path/filepath"
	"testing"
)

func readMeasuredFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "v0.50.1", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func inventoryProfile() compat.Profile {
	return compat.Profile{InventoryStates: map[string]string{"running": "running", "stopped": "stopped"}}
}

func TestMeasuredInventoryStatusAndDetached(t *testing.T) {
	for _, tc := range []struct {
		file, status string
	}{
		{"inventory-running.json", "running"},
		{"inventory-stopped.json", "stopped"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			items, err := decodeInventory(readMeasuredFixture(t, tc.file))
			if err != nil || len(items) != 1 || items[0].ID != "42e3e9d3-6e79-46e5-ad17-a5d5bba50363" || items[0].State != tc.status || !items[0].Detached {
				t.Fatalf("observed inventory = %+v, %v", items, err)
			}
		})
	}
	items, err := decodeInventory([]byte(`[{"id":"owned-id","name":"owned","status":"stopped"}]`))
	if err != nil || len(items) != 1 || items[0].Detached {
		t.Fatalf("stopped sandbox without detached flag = %+v, %v", items, err)
	}
}

func TestObservedMountedWorkspaceIsNotMountless(t *testing.T) {
	items, err := decodeInventory(readMeasuredFixture(t, "inventory-mounted.json"))
	if err != nil || len(items) != 1 || len(items[0].Workspaces) != 1 {
		t.Fatalf("mounted sandbox inventory = %+v, %v", items, err)
	}
}

func TestInventoryKeepsUnrelatedAttachedOrUnknownState(t *testing.T) {
	items, err := decodeInventory([]byte(`[{"id":"owned-id","name":"owned","status":"running","detached":true},{"id":"other-id","name":"unrelated","status":"waking","detached":false}]`))
	if err != nil || len(items) != 2 || items[0].State != "running" || items[1].State != "waking" || items[1].Detached {
		t.Fatalf("unrelated attached sandbox blocked inventory: %+v, %v", items, err)
	}
}

func TestInventoryRejectsMalformedOrConflictingIdentity(t *testing.T) {
	cases := []struct {
		name string
		data string
		code string
	}{
		{"missing id", `[{"name":"owned","status":"stopped"}]`, "E_SBX_SCHEMA"},
		{"missing name", `[{"id":"id","status":"stopped"}]`, "E_SBX_SCHEMA"},
		{"missing status", `[{"id":"id","name":"owned","state":"stopped"}]`, "E_SBX_SCHEMA"},
		{"duplicate id", `[{"id":"id","name":"owned","status":"stopped"},{"id":"id","name":"other","status":"stopped"}]`, "E_SBX_SCHEMA"},
		{"duplicate name", `[{"id":"id","name":"owned","status":"stopped"},{"id":"other","name":"owned","status":"stopped"}]`, "E_SBX_SCHEMA"},
		{"duplicate id field", `[{"id":"id","id":"other","name":"owned","status":"stopped"}]`, "E_SBX_SCHEMA"},
		{"duplicate name field", `[{"id":"id","name":"owned","name":"other","status":"stopped"}]`, "E_SBX_SCHEMA"},
		{"duplicate status field", `[{"id":"id","name":"owned","status":"stopped","status":"running","detached":true}]`, "E_SBX_SCHEMA"},
		{"detached wrong type", `[{"id":"id","name":"owned","status":"stopped","detached":"false"}]`, "E_SBX_SCHEMA"},
		{"workspaces null", `[{"id":"id","name":"owned","status":"stopped","workspaces":null}]`, "E_SBX_SCHEMA"},
		{"workspaces object", `[{"id":"id","name":"owned","status":"stopped","workspaces":{}}]`, "E_SBX_SCHEMA"},
		{"workspace empty", `[{"id":"id","name":"owned","status":"stopped","workspaces":[""]}]`, "E_SBX_SCHEMA"},
		{"null entry", `[null]`, "E_SBX_SCHEMA"},
		{"null inventory", `null`, "E_SBX_SCHEMA"},
		{"malformed inventory", `[{`, "E_SBX_SCHEMA"},
		{"non-array inventory", `{}`, "E_SBX_SCHEMA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeInventory([]byte(tc.data))
			if e, ok := err.(*compat.Error); !ok || e.Code != tc.code {
				t.Fatalf("inventory error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestMeasuredInspectionRequiresMountlessExactIdentity(t *testing.T) {
	inspected, err := decodeInspection(readMeasuredFixture(t, "inspect-mountless.json"), inventoryProfile(), "devsy-qual-20261104")
	if err != nil || inspected.ID != "" || inspected.Name != "devsy-qual-20261104" || inspected.State != "running" || inspected.ImageDigest != "sha256:b178ca62ec2e79af2a044e3ec321bdb72188690e1c15d573ea5524b397554d3d" || inspected.RuntimeMounts == nil || len(inspected.RuntimeMounts) != 0 {
		t.Fatalf("observed inspection = %+v, %v", inspected, err)
	}
	cases := []struct {
		name string
		data string
		code string
	}{
		{"wrong name", `{"name":"other","state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_OWNERSHIP_MISMATCH"},
		{"empty optional id", `{"id":"","name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_SBX_SCHEMA"},
		{"null optional id", `{"id":null,"name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_SBX_SCHEMA"},
		{"duplicate inspect name", `{"name":"owned","name":"other","state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_SBX_SCHEMA"},
		{"missing name", `{"state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_SBX_SCHEMA"},
		{"missing state", `{"name":"owned","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_SBX_SCHEMA"},
		{"unknown state", `{"name":"owned","state":"changing","image_digest":"sha256:abc","runtime_mounts":[]}`, "E_SBX_STATE_UNKNOWN"},
		{"missing digest", `{"name":"owned","state":"stopped","runtime_mounts":[]}`, "E_SBX_SCHEMA"},
		{"missing mounts", `{"name":"owned","state":"stopped","image_digest":"sha256:abc"}`, "E_SBX_SCHEMA"},
		{"null mounts", `{"name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":null}`, "E_SBX_SCHEMA"},
		{"mounted", `{"name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":[{"source":"/tmp/host"}]}`, "E_OWNERSHIP_MISMATCH"},
		{"malformed mounts", `{"name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":{}}`, "E_SBX_SCHEMA"},
		{"null inspection", `null`, "E_SBX_SCHEMA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeInspection([]byte(tc.data), inventoryProfile(), "owned")
			if e, ok := err.(*compat.Error); !ok || e.Code != tc.code {
				t.Fatalf("inspection error = %v, want %s", err, tc.code)
			}
		})
	}
}
