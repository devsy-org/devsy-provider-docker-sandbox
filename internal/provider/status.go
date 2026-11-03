package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"os"
)

func (p Provider) Status(ctx context.Context) (string, error) {
	r, e := p.load()
	if e != nil {
		if errors.Is(e, os.ErrNotExist) || errorCode(e) == "E_STATE_MISSING" {
			return "NotFound", nil
		}
		return "", e
	}
	if e = p.live(ctx, false); e != nil {
		return "", e
	}
	lock, e := p.Store.TryAcquire("mutation", false)
	if e != nil {
		if !errors.Is(e, context.Canceled) {
			return "", compat.Wrap("E_STORAGE", e)
		}
		return "Busy", nil
	}
	if closeErr := lock.Close(); closeErr != nil {
		return "", compat.Wrap("E_STORAGE", closeErr)
	}
	if e = p.guardPrior(r); e != nil {
		return "", e
	}
	item, found, e := p.observe(ctx, r)
	if e != nil {
		return "", e
	}
	if !found {
		if r.SandboxID != "" {
			return "", compat.Fail("E_MACHINE_MISSING", "recorded backend ID unavailable")
		}
		if r.Operation != nil {
			return "", compat.Fail("E_OPERATION_UNCERTAIN", "missing machine during active operation")
		}
		return "NotFound", nil
	}
	backendState := p.Profile.InventoryStates[item.State]
	if r.Operation != nil || backendState == "transitional" || r.Phase == "creating" || r.Phase == "starting" || r.Phase == "stopping" || r.Phase == "deleting" {
		return "Busy", nil
	}
	if backendState == "failed" {
		return "", compat.Fail("E_SBX_STATE_UNKNOWN", "backend entered a failed state")
	}
	if r.SandboxID == "" {
		return "", compat.Fail("E_OWNERSHIP_MISMATCH", "backend ID not recorded")
	}
	if backendState == "stopped" {
		return "Stopped", nil
	}
	if r.Phase == "ready" && r.DesiredState == "running" && backendState == "running" {
		sessions, leaseErr := p.Store.TryAcquire("sessions", false)
		if leaseErr == nil {
			if closeErr := sessions.Close(); closeErr != nil {
				return "", compat.Wrap("E_STORAGE", closeErr)
			}
			if e = p.unsettled(r); e != nil {
				return "", e
			}
		} else if !errors.Is(leaseErr, context.Canceled) {
			return "", compat.Wrap("E_STORAGE", leaseErr)
		}
		return "Running", nil
	}
	return "", compat.Fail("E_OWNERSHIP_MISMATCH", "backend state conflicts with recorded machine intent")
}
