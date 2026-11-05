package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

const (
	testUnitStopped  = "unit-stopped"
	testOtherVersion = "v0.50.1"
	testSettingsStop = "settings read reached"
)

type versionBackend struct {
	simulatedInventory
	version    sbx.Version
	versionErr error
	inventory  error
	state      *string
	removed    *bool
	lenient    *[]bool
	output     string
	executable string
	store      state.Store
}

func (b versionBackend) Version(context.Context) (sbx.Version, error) {
	return b.version, b.versionErr
}

func (b versionBackend) Inventory(context.Context, compat.Profile) (sbx.Inventory, error) {
	if b.inventory != nil {
		return nil, b.inventory
	}
	if *b.removed {
		return sbx.Inventory{}, nil
	}
	item := b.sandbox
	item.State = *b.state
	return sbx.Inventory{item}, nil
}

func (b versionBackend) Inspect(
	_ context.Context,
	_ compat.Profile,
	name string,
	_ bool,
) (sbx.Inspection, error) {
	return sbx.Inspection{
		Name:          name,
		State:         *b.state,
		ImageDigest:   testUnitImage,
		RuntimeMounts: []json.RawMessage{},
	}, nil
}

func (b versionBackend) ShellSettings(_ context.Context, _ compat.Profile, lenient bool) error {
	*b.lenient = append(*b.lenient, lenient)
	return errors.New(testSettingsStop)
}

func (b versionBackend) Start(
	ctx context.Context,
	request sbx.StartRequest,
) (*sbx.Process, error) {
	switch request.Args[0] {
	case "stop":
		*b.state = testUnitStopped
	case "rm":
		*b.removed = true
	}
	return (sbx.Client{Path: b.executable, Store: b.store}).Start(ctx, sbx.StartRequest{
		Args: []string{"-test.run=^TestCommandChildHelper$"},
		Extra: []string{
			"PROVIDER_COMMAND_TEST_CHILD=output",
			"PROVIDER_COMMAND_TEST_OUTPUT=" + b.output,
		},
		Stdin: request.Stdin, Stdout: request.Stdout, Stderr: request.Stderr,
	})
}

type versionFixture struct {
	p       Provider
	r       state.Record
	warned  *bytes.Buffer
	backend versionBackend
}

func versionProfile(t *testing.T, qualified bool) compat.Profile {
	t.Helper()
	p := compat.Profile{
		QualifiedSBXVersions:    []string{testOtherVersion},
		QualifiedSBXRevisions:   map[string]string{testOtherVersion: "measured-revision"},
		QualifiedDaemonVersions: map[string][]string{testOtherVersion: {testOtherVersion}},
		DaemonSchema:            "unit-only",
		InventoryStates: map[string]string{
			testUnitRunning: stateRunning,
			testUnitStopped: stateStopped,
		},
		OperationResultSchema: compat.OperationResultSchemaV0501,
		ShellSettingSchema:    compat.ShellSettingSchemaV0501,
		MinimumDevsyVersion:   "1.2.3",
	}
	if qualified {
		p.QualifiedSBXVersions = []string{testUnitClient}
		p.QualifiedSBXRevisions = map[string]string{testUnitClient: testUnitRevision}
		p.QualifiedDaemonVersions = map[string][]string{testUnitClient: {"unit-daemon"}}
	}
	return p
}

func newVersionFixture(t *testing.T, qualified bool) versionFixture {
	t.Helper()
	return buildVersionFixture(t, qualified, true)
}

func buildVersionFixture(t *testing.T, qualified, saved bool) versionFixture {
	t.Helper()
	p := localProvider(t)
	p.Profile = versionProfile(t, qualified)
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseReady, stateRunning, testUnitID, nil
	if saved {
		requireNoError(t, p.Store.Save(r))
	}
	executable, err := os.Executable()
	requireNoError(t, err)
	running, removed := testUnitRunning, false
	backend := versionBackend{
		simulatedInventory: simulatedInventory{
			sandbox: sbx.Sandbox{
				ID:       r.SandboxID,
				Name:     r.SandboxName,
				State:    testUnitRunning,
				Detached: true,
			},
		},
		state:      &running,
		removed:    &removed,
		lenient:    &[]bool{},
		executable: executable,
		store:      p.Store,
	}
	backend.version.Client.Version = testUnitClient
	backend.version.Client.Revision = testUnitRevision
	backend.version.Server = json.RawMessage(`{"version":"unit-daemon","state":"running"}`)
	warned := &bytes.Buffer{}
	p.Backend = backend
	p.Warner = compat.NewWarner(warned)
	return versionFixture{p: p, r: r, warned: warned, backend: backend}
}

func (f versionFixture) withResult(action string) versionFixture {
	f.backend.output = `[{"action":"` + action + `","resource":"sandbox","name":"` +
		f.r.SandboxName + `","status":"done"}]`
	f.p.Backend = f.backend
	return f
}

func requireOneVersionWarning(t *testing.T, warned *bytes.Buffer) {
	t.Helper()
	text := warned.String()
	for _, want := range []string{
		"warning: sbx " + testUnitClient + " revision unit-revision daemon unit-daemon",
		"qualified: sbx " + testOtherVersion + " revision measured-revision",
		"file an issue",
		compat.IssuesURL,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("warning %q lacks %q", text, want)
		}
	}
	if strings.Count(text, "warning:") != 1 || strings.Count(text, "\n") != 1 {
		t.Fatalf("version warning was not emitted exactly once: %q", text)
	}
}

func TestUnknownVersionStatusWarnsOnceAndProceeds(t *testing.T) {
	f := newVersionFixture(t, false)
	for range 2 {
		status, err := f.p.Status(context.Background())
		if err != nil || status != testStatusRunning {
			t.Fatalf("status = %q, %v", status, err)
		}
	}
	requireOneVersionWarning(t, f.warned)
}

func TestQualifiedVersionNeverWarns(t *testing.T) {
	f := newVersionFixture(t, true)
	status, err := f.p.Status(context.Background())
	if err != nil || status != testStatusRunning || f.warned.Len() != 0 {
		t.Fatalf("status = %q, %v, warning %q", status, err, f.warned.String())
	}
}

func TestUnknownVersionStopProceedsWithLenientResult(t *testing.T) {
	f := newVersionFixture(t, false)
	f = f.withResult("stopped")
	requireNoError(t, f.p.Stop(context.Background()))
	stored, err := f.p.Store.Load()
	requireNoError(t, err)
	if stored.Phase != stateStopped {
		t.Fatalf("phase after stop = %s", stored.Phase)
	}
	requireOneVersionWarning(t, f.warned)
}

func TestQualifiedVersionStopKeepsStrictResult(t *testing.T) {
	f := newVersionFixture(t, true)
	f = f.withResult("stopped")
	if code := errorCode(f.p.Stop(context.Background())); code != codeSBXSchema {
		t.Fatalf("qualified tuple accepted an unknown result field: %s", code)
	}
}

func TestUnknownVersionDeleteProceedsWithLenientResult(t *testing.T) {
	f := newVersionFixture(t, false)
	f = f.withResult("removed")
	requireNoError(t, f.p.Delete(context.Background()))
	stored, err := f.p.Store.Load()
	requireNoError(t, err)
	if stored.Phase != phaseDeleted {
		t.Fatalf("phase after delete = %s", stored.Phase)
	}
	requireOneVersionWarning(t, f.warned)
}

func TestQualifiedVersionDeleteKeepsStrictResult(t *testing.T) {
	f := newVersionFixture(t, true)
	f = f.withResult("removed")
	if code := errorCode(f.p.Delete(context.Background())); code != codeSBXSchema {
		t.Fatalf("qualified tuple accepted an unknown result field: %s", code)
	}
}

func TestUnknownVersionRecoverOperationProceeds(t *testing.T) {
	f := newVersionFixture(t, false)
	f.r.Operation = &state.Operation{ID: "operation", Kind: kindDelete, Phase: phaseUncertain}
	requireNoError(t, f.p.Store.Save(f.r))
	*f.backend.removed = true
	requireNoError(t, f.p.RecoverOperation(context.Background(), "operation"))
	stored, err := f.p.Store.Load()
	requireNoError(t, err)
	if stored.Phase != phaseDeleted || stored.Operation != nil {
		t.Fatalf("recovered record = %+v", stored)
	}
	requireOneVersionWarning(t, f.warned)
}

func TestUnknownVersionCreateProceedsWithLenientSettings(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("test-only Devsy version executable uses a POSIX shell")
	}
	f := buildVersionFixture(t, false, false)
	f.p.Config.Devsy = fakeDevsy(t)
	f.p.Config.Spec = config.Spec{CPUs: 1}
	*f.backend.removed = true
	err := f.p.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), testSettingsStop) {
		t.Fatalf("create did not reach the settings read: %v", err)
	}
	if got := *f.backend.lenient; len(got) != 1 || !got[0] {
		t.Fatalf("settings were not read leniently: %v", got)
	}
	requireOneVersionWarning(t, f.warned)
}

func fakeDevsy(t *testing.T) string {
	t.Helper()
	devsy := filepath.Join(t.TempDir(), "devsy-version")
	requireNoError(t, os.WriteFile(devsy, []byte("#!/bin/sh\nprintf 'v1.2.3\\n'\n"), 0o600))
	root, err := os.OpenRoot(filepath.Dir(devsy))
	requireNoError(t, err)
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	requireNoError(t, root.Chmod(filepath.Base(devsy), 0o700))
	return devsy
}

func TestUnknownVersionStillRejectsUndecodableOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*versionFixture)
		code   string
	}{
		{"daemon without version", func(f *versionFixture) {
			f.backend.version.Server = json.RawMessage(`{"state":"running"}`)
		}, codeSBXVersion},
		{"daemon not running", func(f *versionFixture) {
			f.backend.version.Server = json.RawMessage(`{"version":"unit-daemon","state":"stopped"}`)
		}, codeSBXVersion},
		{"daemon not an object", func(f *versionFixture) {
			f.backend.version.Server = json.RawMessage(`"running"`)
		}, codeSBXVersion},
		{"undecodable inventory", func(f *versionFixture) {
			f.backend.inventory = compat.Fail(codeSBXSchema, "invalid sbx inventory JSON")
		}, codeSBXSchema},
		{"undecodable version", func(f *versionFixture) {
			f.backend.versionErr = compat.Fail(codeSBXSchema, "invalid sbx version JSON")
		}, codeSBXSchema},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVersionFixture(t, false)
			tc.mutate(&f)
			f.p.Backend = f.backend
			for name, run := range map[string]func() error{
				"status": func() error { _, err := f.p.Status(context.Background()); return err },
				"stop":   func() error { return f.p.Stop(context.Background()) },
				"delete": func() error { return f.p.Delete(context.Background()) },
			} {
				if code := errorCode(run()); code != tc.code {
					t.Fatalf("%s error code = %s, want %s", name, code, tc.code)
				}
			}
		})
	}
}

func doctorDiagnosis(t *testing.T, qualified bool) (Diagnosis, versionFixture) {
	t.Helper()
	f := newVersionFixture(t, qualified)
	f.p.Config.SBXPath = "unit-sbx"
	result, err := f.p.Doctor(context.Background())
	requireNoError(t, err)
	var parsed Diagnosis
	requireNoError(t, json.Unmarshal(result, &parsed))
	parsed.Warnings = slices.DeleteFunc(parsed.Warnings, func(w string) bool {
		return !strings.Contains(w, "not a qualified version")
	})
	return parsed, f
}

func TestDoctorReportsUnknownVersionAsWarning(t *testing.T) {
	parsed, f := doctorDiagnosis(t, false)
	if parsed.CompatibilityState != compatibilityWarning || len(parsed.Warnings) != 1 ||
		parsed.BackendError != "" {
		t.Fatalf("doctor = %+v", parsed)
	}
	if !strings.Contains(parsed.Warnings[0], compat.IssuesURL) {
		t.Fatalf("doctor warning = %q", parsed.Warnings[0])
	}
	if parsed.SBX == nil || parsed.SBX.Client != testUnitClient {
		t.Fatalf("doctor omitted the observed sbx tuple: %+v", parsed.SBX)
	}
	if f.warned.Len() != 0 {
		t.Fatalf("doctor wrote to the warning channel: %q", f.warned.String())
	}
}

func TestDoctorReportsQualifiedVersionWithoutWarning(t *testing.T) {
	parsed, _ := doctorDiagnosis(t, true)
	if parsed.CompatibilityState != compatibilityQualified || len(parsed.Warnings) != 0 ||
		parsed.BackendError != "" {
		t.Fatalf("doctor = %+v", parsed)
	}
}
