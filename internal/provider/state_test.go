package provider

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

const testPausedState = "unit-paused"

func unknownStateFixture(t *testing.T) versionFixture {
	t.Helper()
	f := newVersionFixture(t, true)
	*f.backend.state = testPausedState
	return f
}

func requireOneStateWarning(t *testing.T, f versionFixture) {
	t.Helper()
	text := f.warned.String()
	for _, want := range []string{
		"warning: sandbox state \"" + testPausedState + "\" is not recognized",
		"verifies the result from the inventory",
		compat.IssuesURL,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("warning %q lacks %q", text, want)
		}
	}
	if strings.Count(text, "warning:") != 1 {
		t.Fatalf("state warning was not emitted exactly once: %q", text)
	}
}

func TestStopProceedsOnUnrecognizedState(t *testing.T) {
	f := unknownStateFixture(t).withExactResult("stopped")
	requireNoError(t, f.p.Stop(context.Background()))
	stored, err := f.p.Store.Load()
	requireNoError(t, err)
	if stored.Phase != stateStopped || *f.backend.state != testUnitStopped {
		t.Fatalf("stop left phase %s and backend state %s", stored.Phase, *f.backend.state)
	}
	requireOneStateWarning(t, f)
}

func TestStopStillRequiresIdentityOnUnrecognizedState(t *testing.T) {
	f := unknownStateFixture(t).withExactResult("stopped")
	f.backend.sandbox.ID = testOtherID
	f.p.Backend = f.backend
	if code := errorCode(f.p.Stop(context.Background())); code != codeOwnershipMismatch {
		t.Fatalf("stop with a different ID error code = %s", code)
	}
	if *f.backend.state != testPausedState {
		t.Fatal("stop issued a command for a sandbox with a different ID")
	}
}

func TestNotRunningAcceptsStoppedAndUnrecognizedStates(t *testing.T) {
	p := Provider{Profile: versionProfile(t, true)}
	for backendState, want := range map[string]bool{
		testUnitStopped: true,
		testPausedState: true,
		testUnitRunning: false,
	} {
		if got := p.notRunning(backendState); got != want {
			t.Fatalf("notRunning(%q) = %t", backendState, got)
		}
	}
	p.Profile.InventoryStates[testPausedState] = stateTransitional
	if p.notRunning(testPausedState) {
		t.Fatal("transitional state treated as not running")
	}
}

func TestDeleteProceedsOnUnrecognizedState(t *testing.T) {
	f := unknownStateFixture(t).withExactResult("removed")
	requireNoError(t, f.p.Delete(context.Background()))
	stored, err := f.p.Store.Load()
	requireNoError(t, err)
	if stored.Phase != phaseDeleted || !*f.backend.removed {
		t.Fatalf("delete left phase %s removed=%t", stored.Phase, *f.backend.removed)
	}
	requireOneStateWarning(t, f)
}

func TestDeleteStillRequiresIdentityOnUnrecognizedState(t *testing.T) {
	f := unknownStateFixture(t).withExactResult("removed")
	f.backend.sandbox.ID = testOtherID
	f.p.Backend = f.backend
	if code := errorCode(f.p.Delete(context.Background())); code != codeOwnershipMismatch {
		t.Fatalf("delete with a different ID error code = %s", code)
	}
	if *f.backend.removed {
		t.Fatal("delete removed a sandbox with a different ID")
	}
}

func TestRecoverOperationProceedsOnUnrecognizedState(t *testing.T) {
	f := unknownStateFixture(t)
	f.r.Operation = &state.Operation{ID: "operation", Kind: kindStop, Phase: phaseUncertain}
	requireNoError(t, f.p.Store.Save(f.r))
	err := f.p.RecoverOperation(context.Background(), "operation")
	if code := errorCode(err); code != codeOperationUncertain {
		t.Fatalf("recover error code = %s: %v", code, err)
	}
	stored, loadErr := f.p.Store.Load()
	requireNoError(t, loadErr)
	if stored.Operation != nil || stored.Phase != phaseFailed {
		t.Fatalf("recovered record = %+v", stored)
	}
	requireOneStateWarning(t, f)
}

func TestStatusFailsClosedOnUnrecognizedState(t *testing.T) {
	f := unknownStateFixture(t)
	status, err := f.p.Status(context.Background())
	if status != "" || errorCode(err) != codeSBXStateUnknown {
		t.Fatalf("status = %q, %v", status, err)
	}
	if f.warned.Len() != 0 {
		t.Fatalf("status warned instead of failing: %q", f.warned.String())
	}
}

func TestCreateAndStartFailClosedOnUnrecognizedState(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("test-only Devsy version executable uses a POSIX shell")
	}
	for name, run := range map[string]func(Provider) error{
		"start":  func(p Provider) error { return p.Start(context.Background()) },
		"create": func(p Provider) error { return p.Create(context.Background()) },
	} {
		t.Run(name, func(t *testing.T) {
			f := unknownStateFixture(t)
			f.p.Config.Devsy = fakeDevsy(t)
			f.p.Config.Spec = config.Spec{CPUs: 1}
			if code := errorCode(run(f.p)); code != codeSBXStateUnknown {
				t.Fatalf("%s error code = %s", name, code)
			}
			if f.warned.Len() != 0 {
				t.Fatalf("%s warned instead of failing: %q", name, f.warned.String())
			}
		})
	}
}

func (f versionFixture) withExactResult(action string) versionFixture {
	f.backend.output = `[{"action":"` + action + `","resource":"sandbox","name":"` +
		f.r.SandboxName + `"}]`
	f.p.Backend = f.backend
	return f
}
