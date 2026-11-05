package provider

import (
	"context"
	"errors"
	"os"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func (p Provider) Status(ctx context.Context) (string, error) {
	r, e := p.load()
	if e != nil {
		return statusLoadError(e)
	}
	if e = p.live(ctx, false); e != nil {
		return "", e
	}
	if status, done, e := p.statusMutationLock(); done {
		return status, e
	}
	if e = p.guardPrior(r); e != nil {
		return "", e
	}
	item, found, e := p.observe(ctx, r)
	if e != nil {
		return "", e
	}
	if !found {
		return missingStatus(r)
	}
	return p.observedStatus(r, p.Profile.InventoryStates[item.State])
}

func statusLoadError(e error) (string, error) {
	if errors.Is(e, os.ErrNotExist) || errorCode(e) == "E_STATE_MISSING" {
		return "NotFound", nil
	}
	return "", e
}

func (p Provider) statusMutationLock() (string, bool, error) {
	lock, e := p.Store.TryAcquire("mutation", false)
	if e != nil {
		if !errors.Is(e, context.Canceled) {
			return "", true, compat.Wrap("E_STORAGE", e)
		}
		return "Busy", true, nil
	}
	if closeErr := lock.Close(); closeErr != nil {
		return "", true, compat.Wrap("E_STORAGE", closeErr)
	}
	return "", false, nil
}

func missingStatus(r state.Record) (string, error) {
	if r.SandboxID != "" {
		return "", compat.Fail("E_MACHINE_MISSING", "recorded backend ID unavailable")
	}
	if r.Operation != nil {
		return "", compat.Fail(codeOperationUncertain, "missing machine during active operation")
	}
	return "NotFound", nil
}

func (p Provider) observedStatus(r state.Record, backendState string) (string, error) {
	if statusBusy(r, backendState) {
		return "Busy", nil
	}
	if backendState == phaseFailed {
		return "", compat.Fail("E_SBX_STATE_UNKNOWN", "backend entered a failed state")
	}
	if r.SandboxID == "" {
		return "", compat.Fail(codeOwnershipMismatch, "backend ID not recorded")
	}
	if backendState == stateStopped {
		return "Stopped", nil
	}
	if r.Phase == phaseReady && r.DesiredState == stateRunning && backendState == stateRunning {
		return p.runningStatus(r)
	}
	return "", compat.Fail(
		codeOwnershipMismatch,
		"backend state conflicts with recorded machine intent",
	)
}

func statusBusy(r state.Record, backendState string) bool {
	if r.Operation != nil || backendState == stateTransitional {
		return true
	}
	switch r.Phase {
	case "creating", phaseStarting, "stopping", "deleting":
		return true
	default:
		return false
	}
}

func (p Provider) runningStatus(r state.Record) (string, error) {
	sessions, leaseErr := p.Store.TryAcquire("sessions", false)
	if leaseErr == nil {
		if closeErr := sessions.Close(); closeErr != nil {
			return "", compat.Wrap("E_STORAGE", closeErr)
		}
		if e := p.unsettled(r); e != nil {
			return "", e
		}
	} else if !errors.Is(leaseErr, context.Canceled) {
		return "", compat.Wrap("E_STORAGE", leaseErr)
	}
	return "Running", nil
}
