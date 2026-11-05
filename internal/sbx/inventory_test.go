package sbx

import (
	"os"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func readMeasuredFixture(t *testing.T, name string) []byte {
	t.Helper()
	root, err := os.OpenRoot("testdata/v0.50.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func inventoryProfile() compat.Profile {
	return compat.Profile{
		InventoryStates: map[string]string{testRunning: testRunning, testStopped: testStopped},
	}
}

func TestMeasuredInventoryStatusAndDetached(t *testing.T) {
	for _, tc := range []struct {
		file, status string
	}{
		{"inventory-running.json", testRunning},
		{"inventory-stopped.json", testStopped},
	} {
		t.Run(tc.status, func(t *testing.T) {
			items, err := decodeInventory(readMeasuredFixture(t, tc.file))
			if err != nil || !matchesMeasuredInventory(items, tc.status) {
				t.Fatalf("observed inventory = %+v, %v", items, err)
			}
		})
	}
	items, err := decodeInventory(
		[]byte(`[{"id":"owned-id","name":"owned","status":"stopped"}]`),
	)
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
	items, err := decodeInventory(
		[]byte(
			`[{"id":"owned-id","name":"owned","status":"running","detached":tr` +
				`ue},{"id":"other-id","name":"unrelated","status":"waking","detach` +
				`ed":false}]`,
		),
	)
	if err != nil || len(items) != 2 || items[0].State != testRunning ||
		items[1].State != "waking" ||
		items[1].Detached {
		t.Fatalf("unrelated attached sandbox blocked inventory: %+v, %v", items, err)
	}
}

func TestInventoryRejectsMalformedOrConflictingIdentity(t *testing.T) {
	cases := inventoryInvalidCases()
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
	inspected, err := decodeInspection(
		readMeasuredFixture(t, "inspect-mountless.json"),
		inventoryProfile(),
		"devsy-qual-20261104",
		false,
	)
	if err != nil || inspected.ID != "" || inspected.Name != "devsy-qual-20261104" ||
		inspected.State != testRunning ||
		inspected.ImageDigest != "sha256:b178ca62ec2e79af2a044e3ec321bdb72188690e1c15d573ea5524b397554d3d" ||
		inspected.RuntimeMounts == nil ||
		len(inspected.RuntimeMounts) != 0 {
		t.Fatalf("observed inspection = %+v, %v", inspected, err)
	}
}

func TestInspectionRejectsInvalidFields(t *testing.T) {
	cases := inspectionInvalidCases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeInspection([]byte(tc.data), inventoryProfile(), testOwned, false)
			if e, ok := err.(*compat.Error); !ok || e.Code != tc.code {
				t.Fatalf("inspection error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestInspectionToleratesUnrecognizedStateOnlyWhenAsked(t *testing.T) {
	const data = `{"name":"owned","state":"paused","image_digest":"sha256:x","runtime_mounts":[]}`
	_, err := decodeInspection([]byte(data), inventoryProfile(), testOwned, false)
	if e, ok := err.(*compat.Error); !ok || e.Code != "E_SBX_STATE_UNKNOWN" {
		t.Fatalf("strict inspection error = %v", err)
	}
	inspected, err := decodeInspection([]byte(data), inventoryProfile(), testOwned, true)
	if err != nil || inspected.State != "paused" {
		t.Fatalf("tolerant inspection = %+v, %v", inspected, err)
	}
	const noState = `{"name":"owned","image_digest":"sha256:x","runtime_mounts":[]}`
	if _, err = decodeInspection([]byte(noState), inventoryProfile(), testOwned, true); err == nil {
		t.Fatal("tolerant inspection accepted a missing state")
	}
}

func matchesMeasuredInventory(items Inventory, status string) bool {
	return len(items) == 1 && items[0].ID == "42e3e9d3-6e79-46e5-ad17-a5d5bba50363" &&
		items[0].State == status &&
		items[0].Detached
}

func inventoryInvalidCases() []struct {
	name string
	data string
	code string
} {
	return append([]struct {
		name string
		data string
		code string
	}{
		{"missing id", `[{"name":"owned","status":"stopped"}]`, testSchema},
		{"missing name", `[{"id":"id","status":"stopped"}]`, testSchema},
		{"missing status", `[{"id":"id","name":"owned","state":"stopped"}]`, testSchema},
		{
			"duplicate id",
			`[{"id":"id","name":"owned","status":"stopped"},{"id":"id","name":` +
				`"other","status":"stopped"}]`,
			testSchema,
		},
		{
			"duplicate name",
			`[{"id":"id","name":"owned","status":"stopped"},{"id":"other","nam` +
				`e":"owned","status":"stopped"}]`,
			testSchema,
		},
		{
			"duplicate id field",
			`[{"id":"id","id":"other","name":"owned","status":"stopped"}]`,
			testSchema,
		},
		{
			"duplicate name field",
			`[{"id":"id","name":"owned","name":"other","status":"stopped"}]`,
			testSchema,
		},
		{
			"duplicate status field",
			`[{"id":"id","name":"owned","status":"stopped","status":"running","detached":true}]`,
			testSchema,
		},
		{
			"detached wrong type",
			`[{"id":"id","name":"owned","status":"stopped","detached":"false"}]`,
			testSchema,
		},
	}, inventoryInvalidCasesMore()...)
}

func inventoryInvalidCasesMore() []struct {
	name string
	data string
	code string
} {
	return []struct {
		name string
		data string
		code string
	}{
		{
			"workspaces null",
			`[{"id":"id","name":"owned","status":"stopped","workspaces":null}]`,
			testSchema,
		},
		{
			"workspaces object",
			`[{"id":"id","name":"owned","status":"stopped","workspaces":{}}]`,
			testSchema,
		},
		{
			"workspace empty",
			`[{"id":"id","name":"owned","status":"stopped","workspaces":[""]}]`,
			testSchema,
		},
		{"null entry", `[null]`, testSchema},
		{"null inventory", testNull, testSchema},
		{"malformed inventory", `[{`, testSchema},
		{"non-array inventory", `{}`, testSchema},
	}
}

func inspectionInvalidCases() []struct {
	name string
	data string
	code string
} {
	return append([]struct {
		name string
		data string
		code string
	}{
		{
			"wrong name",
			`{"name":"other","state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`,
			"E_OWNERSHIP_MISMATCH",
		},
		{
			"empty optional id",
			`{"id":"","name":"owned","state":"stopped","image_digest":"sha256:` +
				`abc","runtime_mounts":[]}`,
			testSchema,
		},
		{
			"null optional id",
			`{"id":null,"name":"owned","state":"stopped","image_digest":"sha25` +
				`6:abc","runtime_mounts":[]}`,
			testSchema,
		},
		{
			"duplicate inspect name",
			`{"name":"owned","name":"other","state":"stopped","image_digest":"` +
				`sha256:abc","runtime_mounts":[]}`,
			testSchema,
		},
		{
			"missing name",
			`{"state":"stopped","image_digest":"sha256:abc","runtime_mounts":[]}`,
			testSchema,
		},
		{
			"missing state",
			`{"name":"owned","image_digest":"sha256:abc","runtime_mounts":[]}`,
			testSchema,
		},
	}, inspectionInvalidCasesMore()...)
}

func inspectionInvalidCasesMore() []struct {
	name string
	data string
	code string
} {
	return []struct {
		name string
		data string
		code string
	}{
		{
			"unknown state",
			`{"name":"owned","state":"changing","image_digest":"sha256:abc","runtime_mounts":[]}`,
			"E_SBX_STATE_UNKNOWN",
		},
		{
			"missing digest",
			`{"name":"owned","state":"stopped","runtime_mounts":[]}`,
			testSchema,
		},
		{
			"missing mounts",
			`{"name":"owned","state":"stopped","image_digest":"sha256:abc"}`,
			testSchema,
		},
		{
			"null mounts",
			`{"name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":null}`,
			testSchema,
		},
		{
			"mounted",
			`{"name":"owned","state":"stopped","image_digest":"sha256:abc","ru` +
				`ntime_mounts":[{"source":"/tmp/host"}]}`,
			"E_OWNERSHIP_MISMATCH",
		},
		{
			"malformed mounts",
			`{"name":"owned","state":"stopped","image_digest":"sha256:abc","runtime_mounts":{}}`,
			testSchema,
		},
		{"null inspection", testNull, testSchema},
	}
}
