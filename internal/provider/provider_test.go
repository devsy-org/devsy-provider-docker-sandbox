package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func localProvider(t *testing.T) Provider {
	t.Helper()
	folder := t.TempDir()
	key, e := config.Key(folder)
	requireNoError(t, e)
	return Provider{
		Store: state.Store{
			Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
			Key:  key,
		},
		Identity: config.MachineIdentity{
			Key:     key,
			Folder:  folder,
			ID:      "machine",
			Context: "context",
		},
	}
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
	requireNoError(t, e)
	if e = p.Store.Save(r); e != nil {
		t.Fatal(e)
	}
	result, e := p.Doctor(context.Background())
	requireNoError(t, e)
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
	requireNoError(t, e)
	id, e := state.Token()
	requireNoError(t, e)
	a := state.Attempt{
		SchemaVersion: 1,
		AttemptID:     id,
		OperationID:   r.Operation.ID,
		MachineKey:    r.MachineKey,
		Generation:    r.Generation,
		Kind:          kindCreate,
		Phase:         phaseStarted,
		Outcome:       outcomeUnknown,
	}
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
	requireNoError(t, e)
	if r.Operation == nil || r.Operation.Phase != phaseUncertain {
		t.Fatal("acknowledging child incorrectly settled backend operation")
	}
	a, e = p.Store.ReadAttempt(id)
	requireNoError(t, e)
	if a.Phase != phaseReconciled {
		t.Fatal("exact attempt not reconciled")
	}
}

type simulatedInventory struct {
	sandbox    sbx.Sandbox
	inspection *sbx.Inspection
	inspected  *int
	others     sbx.Inventory
}

func (b simulatedInventory) Version(context.Context) (sbx.Version, error) {
	var version sbx.Version
	version.Client.Version = testUnitClient
	version.Client.Revision = "unit-revision"
	version.Server = json.RawMessage(`{"version":"unit-daemon","state":"running"}`)
	return version, nil
}

func (b simulatedInventory) Inventory(context.Context, compat.Profile) (sbx.Inventory, error) {
	return append(sbx.Inventory{b.sandbox}, b.others...), nil
}

func (b simulatedInventory) Inspect(
	_ context.Context,
	_ compat.Profile,
	name string,
) (sbx.Inspection, error) {
	if b.inspected != nil {
		(*b.inspected)++
	}
	if b.inspection != nil {
		return *b.inspection, nil
	}
	if name != b.sandbox.Name {
		return sbx.Inspection{}, errors.New("unexpected inspect")
	}
	return sbx.Inspection{
		Name:          name,
		State:         b.sandbox.State,
		ImageDigest:   "sha256:unit-image",
		RuntimeMounts: []json.RawMessage{},
	}, nil
}

func (b simulatedInventory) ShellSettings(context.Context, compat.Profile) error {
	return errors.New("unexpected settings read")
}

func (b simulatedInventory) Start(context.Context, sbx.StartRequest) (*sbx.Process, error) {
	return nil, errors.New("unexpected child launch")
}

type snapshotBackend struct {
	simulatedInventory
	inventory func() (sbx.Inventory, error)
	inspect   func() (sbx.Inspection, error)
}

func (b snapshotBackend) Inventory(context.Context, compat.Profile) (sbx.Inventory, error) {
	return b.inventory()
}

func (b snapshotBackend) Inspect(context.Context, compat.Profile, string) (sbx.Inspection, error) {
	return b.inspect()
}

type observeSnapshotFixture struct {
	provider         Provider
	record           state.Record
	running, stopped sbx.Sandbox
	inspectedStopped sbx.Inspection
}

func newObserveSnapshotFixture(t *testing.T) observeSnapshotFixture {
	p := localProvider(t)
	p.Profile = compat.Profile{
		InventoryStates: map[string]string{stateRunning: stateRunning, stateStopped: stateStopped},
	}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.SandboxID = testOwnedID
	running := sbx.Sandbox{
		ID:       r.SandboxID,
		Name:     r.SandboxName,
		State:    stateRunning,
		Detached: true,
	}
	stopped := running
	stopped.State = stateStopped
	inspectedStopped := sbx.Inspection{
		Name:          r.SandboxName,
		State:         stateStopped,
		ImageDigest:   "sha256:unit-image",
		RuntimeMounts: []json.RawMessage{},
	}
	return observeSnapshotFixture{p, r, running, stopped, inspectedStopped}
}

func TestObserveRetriesRacingSnapshots(t *testing.T) {
	fixture := newObserveSnapshotFixture(t)
	t.Run("state settles between reads", func(t *testing.T) { checkSettledSnapshot(t, fixture) })
	t.Run("removed before inspect", func(t *testing.T) { checkRemovedSnapshot(t, fixture) })
}

func checkSettledSnapshot(t *testing.T, fixture observeSnapshotFixture) {
	p, r := fixture.provider, fixture.record
	running, stopped := fixture.running, fixture.stopped
	inspectedStopped := fixture.inspectedStopped
	inventories, inspections := 0, 0
	p.Backend = snapshotBackend{
		inventory: func() (sbx.Inventory, error) {
			inventories++
			if inventories == 1 {
				return sbx.Inventory{running}, nil
			}
			return sbx.Inventory{stopped}, nil
		},
		inspect: func() (sbx.Inspection, error) {
			inspections++
			return inspectedStopped, nil
		},
	}
	item, found, err := p.observe(context.Background(), r)
	if err != nil || !found || item.State != stateStopped || inventories != 2 ||
		inspections != 2 {
		t.Fatalf(
			"settled snapshot = %+v found=%t inventories=%d inspections=%d error=%v",
			item,
			found,
			inventories,
			inspections,
			err,
		)
	}
}

func checkRemovedSnapshot(t *testing.T, fixture observeSnapshotFixture) {
	p, r, running := fixture.provider, fixture.record, fixture.running
	inventories, inspections := 0, 0
	p.Backend = snapshotBackend{
		inventory: func() (sbx.Inventory, error) {
			inventories++
			if inventories == 1 {
				return sbx.Inventory{running}, nil
			}
			return sbx.Inventory{}, nil
		},
		inspect: func() (sbx.Inspection, error) {
			inspections++
			return sbx.Inspection{}, compat.Fail(
				"E_SBX_UNAVAILABLE",
				"sandbox disappeared during inspection",
			)
		},
	}
	_, found, err := p.observe(context.Background(), r)
	if err != nil || found || inventories != 2 || inspections != 1 {
		t.Fatalf(
			"removed snapshot found=%t inventories=%d inspections=%d error=%v",
			found,
			inventories,
			inspections,
			err,
		)
	}
}

func TestObserveInspectsOnlyMatchingOwnedSandbox(t *testing.T) {
	p := localProvider(t)
	p.Profile = compat.Profile{
		InventoryStates: map[string]string{stateRunning: stateRunning, stateStopped: stateStopped},
	}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	inspections := 0
	p.Backend = simulatedInventory{
		sandbox:   sbx.Sandbox{ID: "unrelated-id", Name: testOther, State: stateRunning},
		inspected: &inspections,
	}
	if _, found, err := p.observe(
		context.Background(),
		r,
	); !validUnownedObservation(found, inspections, err) {
		t.Fatalf(
			"unowned inventory triggered inspection: found=%t inspections=%d error=%v",
			found,
			inspections,
			err,
		)
	}
	p.Backend = simulatedInventory{
		sandbox:   sbx.Sandbox{ID: "unrelated-id", Name: testOther, State: stateRunning},
		others:    sbx.Inventory{{ID: testOwnedID, Name: r.SandboxName, State: stateStopped}},
		inspected: &inspections,
		inspection: &sbx.Inspection{
			Name:          r.SandboxName,
			State:         stateStopped,
			ImageDigest:   testDigest,
			RuntimeMounts: []json.RawMessage{},
		},
	}
	item, found, err := p.observe(context.Background(), r)
	if err != nil || !found || inspections != 1 || item.ID != testOwnedID ||
		item.Template != testDigest {
		t.Fatalf(
			"owned inspection = %+v found=%t inspections=%d error=%v",
			item,
			found,
			inspections,
			err,
		)
	}
}

func validUnownedObservation(found bool, inspections int, err error) bool {
	return err == nil && !found && inspections == 0
}

func TestObserveRejectsInspectDisagreements(t *testing.T) {
	p := localProvider(t)
	p.Profile = compat.Profile{
		InventoryStates: map[string]string{stateRunning: stateRunning, stateStopped: stateStopped},
	}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.SandboxID = testOwnedID
	owned := sbx.Sandbox{ID: r.SandboxID, Name: r.SandboxName, State: stateStopped}
	base := sbx.Inspection{
		Name:          r.SandboxName,
		State:         stateStopped,
		ImageDigest:   testDigest,
		RuntimeMounts: []json.RawMessage{},
	}
	cases := []struct {
		name   string
		mutate func(*sbx.Inspection)
		code   string
	}{
		{"different name", func(s *sbx.Inspection) { s.Name = testOther }, codeOwnershipMismatch},
		{
			"different optional id",
			func(s *sbx.Inspection) { s.ID = testOtherID },
			codeOwnershipMismatch,
		},
		{
			"different state",
			func(s *sbx.Inspection) { s.State = stateRunning },
			"E_SBX_STATE_UNKNOWN",
		},
		{"missing digest", func(s *sbx.Inspection) { s.ImageDigest = "" }, codeSBXSchema},
		{"missing mounts", func(s *sbx.Inspection) { s.RuntimeMounts = nil }, codeSBXSchema},
		{"mounted sandbox", func(s *sbx.Inspection) {
			s.RuntimeMounts = []json.RawMessage{json.RawMessage(`{"source":"/tmp/host"}`)}
		}, codeOwnershipMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inspected := base
			tc.mutate(&inspected)
			p.Backend = simulatedInventory{sandbox: owned, inspection: &inspected}
			_, found, err := p.observe(context.Background(), r)
			if found || errorCode(err) != tc.code {
				t.Fatalf("observe found=%t error=%v, want %s", found, err, tc.code)
			}
		})
	}
	checkInspectOwnership(t, inspectOwnershipFixture{p, r, owned, base})
}

func TestObserveRejectsUnqualifiedOwnedStateAndAttachedMode(t *testing.T) {
	p := localProvider(t)
	p.Profile = compat.Profile{
		InventoryStates: map[string]string{stateRunning: stateRunning, stateStopped: stateStopped},
	}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.SandboxID = testOwnedID
	for _, tc := range []struct {
		name, state, code string
		detached          bool
	}{
		{"unqualified state", "waking", "E_SBX_STATE_UNKNOWN", true},
		{"attached running", stateRunning, codeSBXSchema, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspections := 0
			p.Backend = simulatedInventory{
				sandbox: sbx.Sandbox{
					ID:       r.SandboxID,
					Name:     r.SandboxName,
					State:    tc.state,
					Detached: tc.detached,
				},
				inspected: &inspections,
			}
			if _, found, err := p.observe(
				context.Background(),
				r,
			); found || errorCode(err) != tc.code ||
				inspections != 0 {
				t.Fatalf(
					"owned sandbox admission found=%t inspections=%d error=%v",
					found,
					inspections,
					err,
				)
			}
		})
	}
}

type inspectOwnershipFixture struct {
	provider Provider
	record   state.Record
	owned    sbx.Sandbox
	base     sbx.Inspection
}

func checkInspectOwnership(t *testing.T, fixture inspectOwnershipFixture) {
	p, r, owned, base := fixture.provider, fixture.record, fixture.owned, fixture.base
	r.Compatibility.TemplateIdentity = "sha256:other"
	p.Backend = simulatedInventory{sandbox: owned, inspection: &base}
	if _, found, err := p.observe(
		context.Background(),
		r,
	); found ||
		errorCode(err) != codeOwnershipMismatch {
		t.Fatalf("changed template digest found=%t error=%v", found, err)
	}
	owned.ID = testOtherID
	p.Backend = simulatedInventory{sandbox: owned, inspection: &base}
	if _, found, err := p.observe(
		context.Background(),
		r,
	); found ||
		errorCode(err) != codeOwnershipMismatch {
		t.Fatalf("changed inventory ID found=%t error=%v", found, err)
	}
	owned.ID = r.SandboxID
	owned.Workspaces = []string{"/tmp/devsy-sbx-qualification/mount-sentinel"}
	inspections := 0
	p.Backend = simulatedInventory{sandbox: owned, inspected: &inspections}
	if _, found, err := p.observe(
		context.Background(),
		r,
	); found || errorCode(err) != codeOwnershipMismatch ||
		inspections != 0 {
		t.Fatalf("host-mounted sandbox found=%t inspections=%d error=%v", found, inspections, err)
	}
}

func TestObserveAllowingAttachedAcceptsFreshlyCreatedRunningSandbox(t *testing.T) {
	p := localProvider(t)
	p.Profile = compat.Profile{
		InventoryStates: map[string]string{stateRunning: stateRunning, stateStopped: stateStopped},
	}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.SandboxID = testOwnedID
	owned := sbx.Sandbox{ID: r.SandboxID, Name: r.SandboxName, State: stateRunning}
	inspection := sbx.Inspection{
		Name:          r.SandboxName,
		State:         stateRunning,
		ImageDigest:   testDigest,
		RuntimeMounts: []json.RawMessage{},
	}
	p.Backend = simulatedInventory{sandbox: owned, inspection: &inspection}
	if _, found, err := p.observe(
		context.Background(),
		r,
	); found ||
		errorCode(err) != codeSBXSchema {
		t.Fatalf("strict observation found=%t error=%v", found, err)
	}
	item, found, err := p.observeAllowingAttached(context.Background(), r)
	if err != nil || !found || item.ID != r.SandboxID {
		t.Fatalf("attached-tolerant observation = %+v found=%t error=%v", item, found, err)
	}
	owned.ID = testOtherID
	p.Backend = simulatedInventory{sandbox: owned, inspection: &inspection}
	if _, found, err := p.observeAllowingAttached(
		context.Background(),
		r,
	); found ||
		errorCode(err) != codeOwnershipMismatch {
		t.Fatalf(
			"attached-tolerant observation accepted a different ID: found=%t error=%v",
			found,
			err,
		)
	}
}

func simulatedProfile(t *testing.T) compat.Profile {
	t.Helper()
	p := compat.Profile{
		QualifiedHosts:          []string{runtime.GOOS + "/" + runtime.GOARCH},
		QualifiedSBXVersions:    []string{testUnitClient},
		QualifiedSBXRevisions:   map[string]string{testUnitClient: "unit-revision"},
		QualifiedDaemonVersions: map[string][]string{testUnitClient: {"unit-daemon"}},
		DaemonSchema:            "unit-only",
		InventoryStates:         map[string]string{testUnitRunning: stateRunning},
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
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseReady, stateRunning, "unit-id", nil
	p.Backend = simulatedInventory{
		sandbox: sbx.Sandbox{
			ID:       r.SandboxID,
			Name:     r.SandboxName,
			State:    testUnitRunning,
			Detached: true,
		},
	}
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attemptID, err := state.Token()
	requireNoError(t, err)
	attempt := state.Attempt{
		SchemaVersion: 1,
		AttemptID:     attemptID,
		MachineKey:    r.MachineKey,
		Generation:    r.Generation,
		SandboxName:   r.SandboxName,
		Epoch:         r.Epoch,
		Kind:          kindCommand,
		Phase:         phaseStarted,
		Outcome:       outcomeUnknown,
	}
	if err := p.Store.SaveAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	lease, err := p.Store.Acquire(context.Background(), "sessions", true)
	requireNoError(t, err)
	status, err := p.Status(context.Background())
	if err != nil || status != "Running" {
		t.Fatalf("live session status = %q, %v", status, err)
	}
	if err := closeLock(lease); err != nil {
		t.Fatal(err)
	}
	status, err = p.Status(context.Background())
	if status != "" || errorCode(err) != codeUncleanAttempt {
		t.Fatalf("orphaned session status = %q, %v", status, err)
	}
}

type uncertainCreateCase struct {
	name   string
	change func(*state.Attempt)
	adopt  bool
}

func uncertainCreateCases() []uncertainCreateCase {
	return []uncertainCreateCase{
		{"started and settled", func(*state.Attempt) {}, true},
		{
			"not started",
			func(a *state.Attempt) { a.StartedAt = nil; a.Phase = phaseReconciled; a.Outcome = outcomeUnknown },
			false,
		},
		{
			"start failed",
			func(a *state.Attempt) { a.StartedAt = nil; a.Outcome = "start_failed" },
			false,
		},
		{"attempt pointer missing", func(*state.Attempt) {}, false},
		{"different operation", func(a *state.Attempt) { a.OperationID = "unrelated" }, false},
		{
			"different generation",
			func(a *state.Attempt) { a.Generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
			false,
		},
		{"different name", func(a *state.Attempt) { a.SandboxName = "unrelated" }, false},
		{"different kind", func(a *state.Attempt) { a.Kind = kindCommand }, false},
		{"missing name", func(a *state.Attempt) { a.SandboxName = "" }, false},
		{"different epoch", func(a *state.Attempt) { a.Epoch++ }, false},
		{"not reaped", func(a *state.Attempt) { a.Phase = "admitted" }, false},
	}
}

func TestUncertainCreationRequiresStartedExactAttempt(t *testing.T) {
	cases := uncertainCreateCases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { testUncertainCreateCase(t, tc) })
	}
}

func testUncertainCreateCase(t *testing.T, tc uncertainCreateCase) {
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	attemptID, err := state.Token()
	requireNoError(t, err)
	r.Operation.Phase = phaseUncertain
	r.Operation.LastAttemptID = attemptID
	now, pid := time.Now().UTC(), 42
	a := state.Attempt{
		SchemaVersion:           1,
		AttemptID:               attemptID,
		OperationID:             r.Operation.ID,
		MachineKey:              r.MachineKey,
		SandboxName:             r.SandboxName,
		Generation:              r.Generation,
		Epoch:                   r.Epoch,
		Kind:                    kindCreate,
		Phase:                   phaseClientReaped,
		StartedAt:               &now,
		CompletedAt:             &now,
		DiagnosticPID:           &pid,
		DiagnosticStartIdentity: now.Format(time.RFC3339Nano),
		Outcome:                 outcomeNormalExit,
	}
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
	p.Backend = simulatedInventory{
		sandbox: sbx.Sandbox{
			ID:       "unit-existing-id",
			Name:     r.SandboxName,
			State:    testUnitRunning,
			Detached: true,
		},
	}
	recoverErr := p.RecoverOperation(context.Background(), r.Operation.ID)
	wantCode := codeOperationUncertain
	if tc.name == "not reaped" {
		wantCode = codeUncleanAttempt
	}
	if errorCode(recoverErr) != wantCode {
		t.Fatalf("recovery error = %v, want %s", recoverErr, wantCode)
	}
	updated, err := p.Store.Load()
	requireNoError(t, err)
	if (updated.SandboxID == "unit-existing-id") != tc.adopt {
		t.Fatalf("adoption = %q, want %t", updated.SandboxID, tc.adopt)
	}
}

func TestReadinessBlocksUnmeasuredSizingBeforeBackendCall(t *testing.T) {
	p := localProvider(t)
	r, err := state.New(
		p.Identity,
		config.Spec{CPUs: 1, RootDiskBytes: 40 << 30, DockerDiskBytes: 40 << 30},
	)
	requireNoError(t, err)
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
	requireNoError(t, err)
	id, err := state.Token()
	requireNoError(t, err)
	r.Operation.LastAttemptID = id
	now, pid, exitCode := time.Now().UTC(), 42, 0
	attempt := state.Attempt{
		SchemaVersion: 1, AttemptID: id, OperationID: r.Operation.ID,
		MachineKey: r.MachineKey, SandboxName: r.SandboxName,
		Generation: r.Generation, Epoch: r.Epoch, Kind: kindCreate,
		Phase: phaseClientReaped, StartedAt: &now, CompletedAt: &now,
		DiagnosticPID: &pid, DiagnosticStartIdentity: now.Format(time.RFC3339Nano),
		Outcome: outcomeNormalExit, ExitCode: &exitCode,
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
	for _, kind := range []string{kindStop, kindDelete, "start"} {
		t.Run(kind, func(t *testing.T) {
			p := localProvider(t)
			r := reapedCreateRecord(t, p)
			operationID, attemptID, epoch := r.Operation.ID, r.Operation.LastAttemptID, r.Epoch
			lock, err := p.begin(
				context.Background(),
				&r,
				transition{"stopping", stateStopped, kind, kind},
			)
			if lock != nil {
				_ = closeLock(lock)
				t.Fatal("incompatible transition acquired a session lock")
			}
			if errorCode(err) != codeOperationUncertain {
				t.Fatalf("incompatible transition error = %v", err)
			}
			stored, err := p.Store.Load()
			requireNoError(t, err)
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
	if err := p.markPriorCreateUncertain(&r); errorCode(err) != codeOperationUncertain {
		t.Fatalf("unsettled creation error = %v", err)
	}
	stored, err := p.Store.Load()
	requireNoError(t, err)
	checkPendingCreateProvenance(t, stored, operationID, attemptID)
	p.Profile = simulatedProfile(t)
	p.Backend = simulatedInventory{}
	if err := p.RecoverOperation(
		context.Background(),
		operationID,
	); errorCode(
		err,
	) != codeOperationUncertain {
		t.Fatalf("backend settlement error = %v", err)
	}
	stored, err = p.Store.Load()
	requireNoError(t, err)
	checkAcknowledgedAbsentCreate(t, stored)
}

func checkPendingCreateProvenance(
	t *testing.T,
	stored state.Record,
	operationID, attemptID string,
) {
	t.Helper()
	if stored.Operation == nil || stored.Operation.Phase != phaseUncertain ||
		stored.Operation.ID != operationID || stored.Operation.LastAttemptID != attemptID {
		t.Fatalf("unsettled creation lost provenance: %+v", stored.Operation)
	}
}

func checkAcknowledgedAbsentCreate(t *testing.T, stored state.Record) {
	t.Helper()
	if stored.Operation != nil || stored.Phase != phaseFailed ||
		stored.LastErrorCode != codeOperationUncertain || stored.SandboxID != "" {
		t.Fatalf("acknowledged absent creation retained pending ownership: %+v", stored)
	}
}

func TestStatusRejectsMissingPreviouslyOwnedMachine(t *testing.T) {
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	p.Backend = simulatedInventory{}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseReady, stateRunning, testOwnedID, nil
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	status, err := p.Status(context.Background())
	if status != "" || errorCode(err) != "E_MACHINE_MISSING" {
		t.Fatalf("missing owned machine status = %q, %v", status, err)
	}
}

func TestBeginDoesNotPersistTransitionWhenDrainFails(t *testing.T) {
	p := localProvider(t)
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseReady, stateRunning, testOwnedID, nil
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	before, err := p.Store.Load()
	requireNoError(t, err)
	lease, err := p.Store.Acquire(context.Background(), "sessions", true)
	requireNoError(t, err)
	defer func() { _ = closeLock(lease) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sessions, err := p.begin(ctx, &r, transition{"deleting", phaseDeleted, kindDelete, "rm"})
	if sessions != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("failed drain returned lock %v, error %v", sessions, err)
	}
	after, err := p.Store.Load()
	requireNoError(t, err)
	if !reflect.DeepEqual(before, after) || r.Epoch != before.Epoch || r.Operation != nil {
		t.Fatalf("failed drain changed durable ownership: before %+v, after %+v", before, after)
	}
	admission, err := p.Store.TryAcquire("admission", false)
	if err != nil {
		t.Fatalf("failed drain retained admission lock: %v", err)
	}
	requireNoError(t, closeLock(admission))
}

func TestBeginPreservesUnsettledOperationAndIntent(t *testing.T) {
	p := localProvider(t)
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseReady, stateRunning, testOwnedID, nil
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	before, err := p.Store.Load()
	requireNoError(t, err)
	id, err := state.Token()
	requireNoError(t, err)
	attempt := state.Attempt{
		SchemaVersion: 1,
		AttemptID:     id,
		MachineKey:    r.MachineKey,
		Generation:    r.Generation,
		SandboxName:   r.SandboxName,
		Epoch:         r.Epoch,
		Kind:          kindCommand,
		Phase:         phaseStarted,
		Outcome:       outcomeUnknown,
	}
	if err := p.Store.SaveAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	sessions, err := p.begin(
		context.Background(),
		&r,
		transition{"deleting", phaseDeleted, kindDelete, "rm"},
	)
	if sessions != nil || errorCode(err) != codeUncleanAttempt {
		t.Fatalf("unsettled attempt returned lock %v, error %v", sessions, err)
	}
	after, err := p.Store.Load()
	requireNoError(t, err)
	if !reflect.DeepEqual(before, after) || r.Epoch != before.Epoch || r.Operation != nil {
		t.Fatalf(
			"unsettled attempt changed durable ownership: before %+v, after %+v",
			before,
			after,
		)
	}
}

func TestStatusDeletedRecordWithMissingOwnedIDIsNotNotFound(t *testing.T) {
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	p.Backend = simulatedInventory{}
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseDeleted, phaseDeleted, testOwnedID, nil
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	status, err := p.Status(context.Background())
	if status != "" || errorCode(err) != "E_MACHINE_MISSING" {
		t.Fatalf("deleted record with missing owned ID status = %q, %v", status, err)
	}
}

func TestDeleteRefusesUnrecordedBackendWithoutLeavingActiveOperation(t *testing.T) {
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	p.Profile.OperationResultSchema = compat.OperationResultSchemaV0501
	r, err := state.New(p.Identity, config.Spec{CPUs: 1})
	requireNoError(t, err)
	if err := p.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	p.Backend = simulatedInventory{
		sandbox: sbx.Sandbox{
			ID:       "unrecorded-id",
			Name:     r.SandboxName,
			State:    testUnitRunning,
			Detached: true,
		},
	}
	if code := errorCode(p.Delete(context.Background())); code != codeOwnershipMismatch {
		t.Fatalf("delete unrecorded backend error code = %s", code)
	}
	after, err := p.Store.Load()
	requireNoError(t, err)
	if after.Operation != nil || after.Phase != phaseFailed || after.SandboxID != "" ||
		after.LastErrorCode != codeOwnershipMismatch {
		t.Fatalf("refused delete persisted incorrect ownership state: %+v", after)
	}
}

type simulatedCommandBackend struct {
	simulatedInventory
	executable string
	store      state.Store
	started    *[][]string
}

func (b simulatedCommandBackend) Start(
	ctx context.Context,
	request sbx.StartRequest,
) (*sbx.Process, error) {
	var extra []string
	if b.started != nil {
		*b.started = append(*b.started, request.Args)
	}
	if !slices.Contains(request.Args, "--interactive") {
		extra = []string{"PROVIDER_COMMAND_TEST_CHILD=terminated"}
	}
	return (sbx.Client{Path: b.executable, Store: b.store}).Start(ctx, sbx.StartRequest{
		Args: []string{"-test.run=^TestCommandChildHelper$"}, Extra: extra,
		Stdin: request.Stdin, Stdout: request.Stdout, Stderr: request.Stderr,
	})
}

func TestCommandChildHelper(_ *testing.T) {
	mode := os.Getenv("PROVIDER_COMMAND_TEST_CHILD")
	if mode == "" {
		return
	}
	if _, err := os.Stdout.Write([]byte(phaseReady)); err != nil {
		os.Exit(2)
	}
	if mode == "terminated" {
		os.Exit(0)
	}
	if mode == "exit" {
		os.Exit(7)
	}
	for {
		time.Sleep(time.Hour)
	}
}

type commandSignalWriter func([]byte) (int, error)

func (w commandSignalWriter) Write(data []byte) (int, error) { return w(data) }

type commandOutcomeCase struct{ name, mode, outcome, code string }

func TestCommandAttemptOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test-only Devsy version executable uses a POSIX shell")
	}
	cases := []commandOutcomeCase{
		{name: "normal nonzero exit", mode: "exit", outcome: outcomeNormalExit},
		{name: "caller cancellation", mode: testWait, outcome: outcomeCancelled},
		{
			name:    "watcher interruption",
			mode:    testWait,
			outcome: "interrupted",
			code:    "E_ACTIVE_SESSIONS",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { testCommandOutcome(t, tc) })
	}
}

func commandOutcomeProvider(t *testing.T, mode string) (Provider, state.Record, *[][]string) {
	t.Helper()
	p := localProvider(t)
	p.Profile = simulatedProfile(t)
	p.Config.Spec = config.Spec{CPUs: 1}
	p.Profile.MinimumDevsyVersion = "1.2.3"
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
	p.Config.Devsy = devsy
	t.Setenv("PROVIDER_COMMAND_TEST_CHILD", mode)
	r, err := state.New(p.Identity, p.Config.Spec)
	requireNoError(t, err)
	r.Phase, r.DesiredState, r.SandboxID, r.Operation = phaseReady, stateRunning, "unit-id", nil
	requireNoError(t, p.Store.Save(r))
	executable, err := os.Executable()
	requireNoError(t, err)
	started := &[][]string{}
	p.Backend = simulatedCommandBackend{
		simulatedInventory: simulatedInventory{
			sandbox: sbx.Sandbox{
				ID:       r.SandboxID,
				Name:     r.SandboxName,
				State:    testUnitRunning,
				Detached: true,
			},
		},
		executable: executable,
		store:      p.Store,
		started:    started,
	}
	return p, r, started
}

func testCommandOutcome(t *testing.T, tc commandOutcomeCase) {
	p, r, started := commandOutcomeProvider(t, tc.mode)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := make(chan struct{}, 1)
	writer := commandSignalWriter(func(data []byte) (int, error) {
		select {
		case ready <- struct{}{}:
		default:
		}
		return len(data), nil
	})
	result := make(chan error, 1)
	go func() { result <- p.Command(ctx, "unit-command", CommandStreams{Stdout: writer, Stderr: io.Discard}) }()
	if tc.mode == testWait {
		waitCommandReady(ctx, t, ready, result)
	}
	switch tc.outcome {
	case outcomeCancelled:
		cancel()
	case "interrupted":
		r.Epoch++
		requireNoError(t, p.Store.Save(r))
	}
	waitCommandOutcome(t, tc, result)
	checkCommandAttempt(t, tc, p, *started)
}

func waitCommandReady(
	ctx context.Context,
	t *testing.T,
	ready <-chan struct{},
	result <-chan error,
) {
	t.Helper()
	select {
	case <-ready:
	case err := <-result:
		t.Fatalf("command exited before child readiness: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func waitCommandOutcome(t *testing.T, tc commandOutcomeCase, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		checkCommandResult(t, tc, err)
	case <-time.After(10 * time.Second):
		t.Fatal("command did not finish")
	}
}

func checkCommandResult(t *testing.T, tc commandOutcomeCase, err error) {
	t.Helper()
	if tc.code != "" {
		if errorCode(err) != tc.code {
			t.Fatalf("command error = %v", err)
		}
	} else if tc.outcome == outcomeCancelled {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("command cancellation = %v", err)
		}
	} else if code, ok := sbx.ExitCode(err); !ok || code != 7 {
		t.Fatalf("command exit = %v", err)
	}
}

func checkCommandAttempt(t *testing.T, tc commandOutcomeCase, p Provider, started [][]string) {
	t.Helper()
	attempts, err := p.Store.Attempts()
	requireNoError(t, err)
	if !validCommandAttempt(attempts, tc) {
		t.Fatalf("attempt outcome = %+v", attempts)
	}
	if tc.outcome == outcomeNormalExit &&
		(attempts[0].ExitCode == nil || *attempts[0].ExitCode != 7) {
		t.Fatalf("normal command exit code = %+v", attempts[0].ExitCode)
	}
	checkCommandTag(t, tc, attempts[0].AttemptID, started)
}

func validCommandAttempt(attempts []state.Attempt, tc commandOutcomeCase) bool {
	return len(attempts) == 1 && attempts[0].Phase == phaseClientReaped &&
		attempts[0].Outcome == tc.outcome && attempts[0].StartedAt != nil && attempts[0].CompletedAt != nil
}

func checkCommandTag(t *testing.T, tc commandOutcomeCase, attemptID string, started [][]string) {
	t.Helper()
	session := sbx.SessionEnv + "=" + attemptID
	if len(started) == 0 || !slices.Contains(started[0], session) {
		t.Fatalf("guest command was not tagged with its attempt: %v", started)
	}
	terminated := len(started) == 2 && slices.Contains(started[1], attemptID) &&
		!slices.Contains(started[1], "--interactive")
	if terminated != (tc.outcome == outcomeCancelled) || (!terminated && len(started) != 1) {
		t.Fatalf("guest termination for outcome %s = %v", tc.outcome, started)
	}
}
