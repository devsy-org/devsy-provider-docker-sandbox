package provider

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

const (
	testPausedState  = "unit-paused"
	testRestingState = "unit-resting"
)

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

func TestStopSettledRule(t *testing.T) {
	p := Provider{Profile: versionProfile(t, true)}
	for _, test := range []struct {
		name, before, current string
		want                  bool
	}{
		{"stopped from running", testUnitRunning, testUnitStopped, true},
		{"stopped from unlisted", testPausedState, testUnitStopped, true},
		{"still running", testUnitRunning, testUnitRunning, false},
		{"same unlisted state", testPausedState, testPausedState, false},
		{"different unlisted state", testPausedState, testRestingState, true},
		{"unlisted state from running", testUnitRunning, testPausedState, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := p.stopSettled(test.before, test.current); got != test.want {
				t.Fatalf("stopSettled(%q, %q) = %t", test.before, test.current, got)
			}
		})
	}
	p.Profile.InventoryStates[testRestingState] = stateTransitional
	if p.stopSettled(testPausedState, testRestingState) {
		t.Fatal("transitional state treated as stopped")
	}
}

func TestStopWaitsWhileUnlistedStateIsUnchanged(t *testing.T) {
	f := unknownStateFixture(t).withExactResult("stopped")
	f.backend.stopState = testPausedState
	f.p.Backend = f.backend
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := f.p.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop error = %v, want the wait to time out", err)
	}
	stored, loadErr := f.p.Store.Load()
	requireNoError(t, loadErr)
	if stored.Phase == stateStopped || stored.Operation == nil ||
		stored.Operation.Phase != phaseUncertain {
		t.Fatalf("unverified stop recorded %+v", stored)
	}
}

func TestStopCompletesWhenUnlistedStateChanges(t *testing.T) {
	f := unknownStateFixture(t).withExactResult("stopped")
	f.backend.stopState = testRestingState
	f.p.Backend = f.backend
	requireNoError(t, f.p.Stop(context.Background()))
	stored, err := f.p.Store.Load()
	requireNoError(t, err)
	if stored.Phase != stateStopped || stored.Operation != nil {
		t.Fatalf("stop recorded %+v", stored)
	}
	text := f.warned.String()
	for _, unlisted := range []string{testPausedState, testRestingState} {
		if !strings.Contains(text, "sandbox state \""+unlisted+"\" is not recognized") {
			t.Fatalf("warnings %q lack state %s", text, unlisted)
		}
	}
	if strings.Count(text, "warning:") != 2 {
		t.Fatalf("expected one warning per unlisted state: %q", text)
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
