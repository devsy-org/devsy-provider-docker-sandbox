package provider

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"io"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func localProvider(t *testing.T) Provider {
	t.Helper()
	folder := t.TempDir()
	key, e := config.Key(folder)
	if e != nil {
		t.Fatal(e)
	}
	return Provider{Store: state.Store{Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"), Key: key}, Identity: config.MachineIdentity{Key: key, Folder: folder, ID: "machine", Context: "context"}}
}
func TestMissingRecordStatusNeverStartsBackend(t *testing.T) {
	p := localProvider(t)
	token, e := p.Status(context.Background())
	if e != nil || token != "NotFound" {
		t.Fatalf("missing status: %s, %v", token, e)
	}
}
func TestDoctorReadsLocalStateWithoutBackend(t *testing.T) {
	p := localProvider(t)
	r, e := state.New(p.Identity, config.Spec{CPUs: 2})
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Store.Save(r); e != nil {
		t.Fatal(e)
	}
	result, e := p.Doctor(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var parsed Diagnosis
	if e = json.Unmarshal(result, &parsed); e != nil {
		t.Fatal(e)
	}
	if parsed.Record == nil || parsed.Record.SandboxName != r.SandboxName {
		t.Fatal("doctor omitted independent recovery record")
	}
}
func TestAcknowledgedClientNeverSettlesBackend(t *testing.T) {
	p := localProvider(t)
	r, e := state.New(p.Identity, config.Spec{CPUs: 2})
	if e != nil {
		t.Fatal(e)
	}
	id, e := state.Token()
	if e != nil {
		t.Fatal(e)
	}
	a := state.Attempt{SchemaVersion: 1, AttemptID: id, OperationID: r.Operation.ID, MachineKey: r.MachineKey, Generation: r.Generation, Kind: "create", Phase: "started", Outcome: "unknown"}
	if e = p.Store.Save(r); e != nil {
		t.Fatal(e)
	}
	if e = p.Store.SaveAttempt(a); e != nil {
		t.Fatal(e)
	}
	if e = p.RecoverAttempt(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	r, e = p.Store.Load()
	if e != nil {
		t.Fatal(e)
	}
	if r.Operation == nil || r.Operation.Phase != "uncertain" {
		t.Fatal("acknowledging child incorrectly settled backend operation")
	}
	a, e = p.Store.ReadAttempt(id)
	if e != nil {
		t.Fatal(e)
	}
	if a.Phase != "reconciled" {
		t.Fatal("exact attempt not reconciled")
	}
}

type simulatedInventory struct{ sandbox sbx.Sandbox }

func (b simulatedInventory) Version(context.Context) (sbx.Version, error) {
	var version sbx.Version
	version.Client.Version = "unit-client"
	version.Client.Revision = "unit-revision"
	version.Server = json.RawMessage(`{"version":"unit-daemon","state":"running"}`)
	return version, nil
}
func (b simulatedInventory) Inventory(context.Context, compat.Profile) (sbx.Inventory, error) {
	return sbx.Inventory{b.sandbox}, nil
}
func (b simulatedInventory) ShellSettings(context.Context, compat.Profile) error {
	return errors.New("unexpected settings read")
}
func (b simulatedInventory) Start(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) (*sbx.Process, error) {
	return nil, errors.New("unexpected child launch")
}
func simulatedProfile(t *testing.T) compat.Profile {
	t.Helper()
	p := compat.Profile{
		QualifiedHosts:          []string{runtime.GOOS + "/" + runtime.GOARCH},
		QualifiedSBXVersions:    []string{"unit-client"},
		QualifiedSBXRevisions:   map[string]string{"unit-client": "unit-revision"},
		QualifiedDaemonVersions: map[string][]string{"unit-client": {"unit-daemon"}},
		DaemonSchema:            "unit-only",
		InventoryStates:         map[string]string{"unit-running": "running"},
	}
	if err := p.Host(); err != nil {
		t.Skipf("unsupported test host: %v", err)
	}
	return p
}
func TestStatusWithActiveSessionAndOrphanedAttempt(t *testing.T) {
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = "ready", "running", "unit-id", nil
	p.Backend = simulatedInventory{sandbox: sbx.Sandbox{ID: r.SandboxID, Name: r.SandboxName, State: "unit-running"}}
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attemptID, err := state.Token()
	if err != nil {
		t.Fatal(err)
	}
	attempt := state.Attempt{SchemaVersion: 1, AttemptID: attemptID, MachineKey: r.MachineKey, Generation: r.Generation, SandboxName: r.SandboxName, Epoch: r.Epoch, Kind: "command", Phase: "started", Outcome: "unknown"}
	if err := p.Store.SaveAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	lease, err := p.Store.Acquire(context.Background(), "sessions", true)
	if err != nil {
		t.Fatal(err)
	}
	status, err := p.Status(context.Background())
	if err != nil || status != "Running" {
		t.Fatalf("live session status = %q, %v", status, err)
	}
	if err := closeLock(lease); err != nil {
		t.Fatal(err)
	}
	status, err = p.Status(context.Background())
	if status != "" || errorCode(err) != "E_UNCLEAN_ATTEMPT" {
		t.Fatalf("orphaned session status = %q, %v", status, err)
	}
}
func TestUncertainCreationRequiresStartedExactAttempt(t *testing.T) {
	cases := []struct {
		name   string
		change func(*state.Attempt)
		adopt  bool
	}{
		{"started and settled", func(*state.Attempt) {}, true},
		{"not started", func(a *state.Attempt) { a.StartedAt = nil; a.Phase = "reconciled"; a.Outcome = "unknown" }, false},
		{"start failed", func(a *state.Attempt) { a.StartedAt = nil; a.Outcome = "start_failed" }, false},
		{"attempt pointer missing", func(*state.Attempt) {}, false},
		{"different operation", func(a *state.Attempt) { a.OperationID = "unrelated" }, false},
		{"different generation", func(a *state.Attempt) { a.Generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }, false},
		{"different name", func(a *state.Attempt) { a.SandboxName = "unrelated" }, false},
		{"different kind", func(a *state.Attempt) { a.Kind = "command" }, false},
		{"missing name", func(a *state.Attempt) { a.SandboxName = "" }, false},
		{"different epoch", func(a *state.Attempt) { a.Epoch++ }, false},
		{"not reaped", func(a *state.Attempt) { a.Phase = "admitted" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := localProvider(t)
			p.Profile = simulatedProfile(t)
			r, err := state.New(p.Identity, config.Spec{CPUs: 1})
			if err != nil {
				t.Fatal(err)
			}
			attemptID, err := state.Token()
			if err != nil {
				t.Fatal(err)
			}
			r.Operation.Phase = "uncertain"
			r.Operation.LastAttemptID = attemptID
			now, pid := time.Now().UTC(), 42
			a := state.Attempt{SchemaVersion: 1, AttemptID: attemptID, OperationID: r.Operation.ID, MachineKey: r.MachineKey, SandboxName: r.SandboxName, Generation: r.Generation, Epoch: r.Epoch, Kind: "create", Phase: "client_reaped", StartedAt: &now, CompletedAt: &now, DiagnosticPID: &pid, DiagnosticStartIdentity: now.Format(time.RFC3339Nano), Outcome: "normal_exit"}
			tc.change(&a)
			if tc.name == "attempt pointer missing" {
				r.Operation.LastAttemptID = ""
			}
			if err := p.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			if err := p.Store.SaveAttempt(a); err != nil {
				t.Fatal(err)
			}
			p.Backend = simulatedInventory{sandbox: sbx.Sandbox{ID: "unit-existing-id", Name: r.SandboxName, State: "unit-running"}}
			recoverErr := p.RecoverOperation(context.Background(), r.Operation.ID)
			wantCode := "E_OPERATION_UNCERTAIN"
			if tc.name == "not reaped" {
				wantCode = "E_UNCLEAN_ATTEMPT"
			}
			if errorCode(recoverErr) != wantCode {
				t.Fatalf("recovery error = %v, want %s", recoverErr, wantCode)
			}
			updated, err := p.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if (updated.SandboxID == "unit-existing-id") != tc.adopt {
				t.Fatalf("adoption = %q, want %t", updated.SandboxID, tc.adopt)
			}
		})
	}
}
func TestReadinessBlocksUnmeasuredSizingBeforeBackendCall(t *testing.T) {
	p := localProvider(t)
	r, err := state.New(p.Identity, config.Spec{CPUs: 1, RootDiskBytes: 40 << 30, DockerDiskBytes: 40 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.readiness(context.Background(), &r); errorCode(err) != "E_GUEST_CAPABILITY" {
		t.Fatalf("missing disk qualification admitted: %v", err)
	}
	attempts, err := p.Store.Attempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("unqualified probe launched: %v, %v", attempts, err)
	}
}

func reapedCreateRecord(t *testing.T, p Provider) state.Record {
	t.Helper()
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	id, err := state.Token()
	if err != nil {
		t.Fatal(err)
	}
	r.Operation.LastAttemptID = id
	now, pid, exitCode := time.Now().UTC(), 42, 0
	attempt := state.Attempt{
		SchemaVersion: 1, AttemptID: id, OperationID: r.Operation.ID,
		MachineKey: r.MachineKey, SandboxName: r.SandboxName,
		Generation: r.Generation, Epoch: r.Epoch, Kind: "create",
		Phase: "client_reaped", StartedAt: &now, CompletedAt: &now,
		DiagnosticPID: &pid, DiagnosticStartIdentity: now.Format(time.RFC3339Nano),
		Outcome: "normal_exit", ExitCode: &exitCode,
	}
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := p.Store.SaveAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPendingCreateProvenanceSurvivesIncompatibleTransition(t *testing.T) {
	for _, kind := range []string{"stop", "delete", "start"} {
		t.Run(kind, func(t *testing.T) {
			p := localProvider(t)
			r := reapedCreateRecord(t, p)
			operationID, attemptID, epoch := r.Operation.ID, r.Operation.LastAttemptID, r.Epoch
			lock, err := p.begin(context.Background(), &r, "stopping", "stopped", kind, kind)
			if lock != nil {
				_ = closeLock(lock)
				t.Fatal("incompatible transition acquired a session lock")
			}
			if errorCode(err) != "E_OPERATION_UNCERTAIN" {
				t.Fatalf("incompatible transition error = %v", err)
			}
			stored, err := p.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if stored.Operation == nil || stored.Operation.ID != operationID ||
				stored.Operation.LastAttemptID != attemptID || stored.Epoch != epoch {
				t.Fatalf("create provenance changed: %+v", stored)
			}
		})
	}
}

func TestAbsentReapedCreateCanBeAcknowledged(t *testing.T) {
	p := localProvider(t)
	r := reapedCreateRecord(t, p)
	operationID, attemptID := r.Operation.ID, r.Operation.LastAttemptID
	if err := p.markPriorCreateUncertain(&r); errorCode(err) != "E_OPERATION_UNCERTAIN" {
		t.Fatalf("unsettled creation error = %v", err)
	}
	stored, err := p.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Operation == nil || stored.Operation.Phase != "uncertain" ||
		stored.Operation.ID != operationID || stored.Operation.LastAttemptID != attemptID {
		t.Fatalf("unsettled creation lost provenance: %+v", stored.Operation)
	}
	p.Profile = simulatedProfile(t)
	p.Backend = simulatedInventory{}
	if err := p.RecoverOperation(context.Background(), operationID); errorCode(err) != "E_OPERATION_UNCERTAIN" {
		t.Fatalf("backend settlement error = %v", err)
	}
	stored, err = p.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Operation != nil || stored.Phase != "failed" ||
		stored.LastErrorCode != "E_OPERATION_UNCERTAIN" || stored.SandboxID != "" {
		t.Fatalf("acknowledged absent creation retained pending ownership: %+v", stored)
	}
}

func TestStatusRejectsMissingPreviouslyOwnedMachine(t *testing.T) {
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	p.Backend = simulatedInventory{}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = "ready", "running", "owned-id", nil
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	status, err := p.Status(context.Background())
	if status != "" || errorCode(err) != "E_MACHINE_MISSING" {
		t.Fatalf("missing owned machine status = %q, %v", status, err)
	}
}
