package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/probe"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"os"
	"time"
)

func (p Provider) Create(ctx context.Context) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if e := p.live(ctx, true); e != nil {
		return e
	}
	if e := p.Profile.RequireDiskSizingQualification(); e != nil {
		return e
	}
	current, e := p.Backend.Version(ctx)
	if e != nil {
		return e
	}
	daemon, e := sbx.ServerVersion(current, p.Profile)
	if e != nil {
		return e
	}
	creationCompatibility := state.Compatibility{SBXVersion: current.Client.Version, SBXRevision: current.Client.Revision, SBXDaemonVersion: daemon}
	mutation, e := p.mutations(ctx)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(mutation)) }()
	r, e := p.load()
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) && errorCode(e) != "E_STATE_MISSING" {
			return e
		}
		r, e = state.New(p.Identity, p.Config.Spec)
		if e != nil {
			return compat.Wrap("E_STORAGE", e)
		}
		r.Compatibility = creationCompatibility
		if e = p.Store.Save(r); e != nil {
			return e
		}
	} else {
		if e = p.guardPrior(r); e != nil {
			return e
		}
		if r.Phase == "deleted" && r.Operation == nil {
			if e = p.Store.Archive(r); e != nil {
				return e
			}
			r, e = state.New(p.Identity, p.Config.Spec)
			if e != nil {
				return compat.Wrap("E_STORAGE", e)
			}
			r.Compatibility = creationCompatibility
			if e = p.Store.Save(r); e != nil {
				return e
			}
		} else if e = p.checkSpec(r); e != nil {
			return e
		}
		if r.SandboxID != "" {
			if r.Phase == "ready" || r.Phase == "stopped" || r.Phase == "failed" {
				return p.startLocked(ctx, &r)
			}
			return compat.Fail("E_OPERATION_UNCERTAIN", "existing machine generation %s cannot be recreated", r.Generation)
		}
	}
	if r.Operation != nil && r.Operation.Phase == "uncertain" {
		return compat.Fail("E_OPERATION_UNCERTAIN", "pending create %s requires backend acknowledgement", r.Operation.ID)
	}
	item, found, e := p.observe(ctx, r)
	if e != nil {
		return e
	}
	if found {
		if r.Operation == nil || r.Operation.Kind != "create" || r.Operation.LastAttemptID == "" {
			return compat.Fail("E_OWNERSHIP_MISMATCH", "backend name exists without established creation provenance")
		}
		if e := p.requireCreateAttempt(r); e != nil {
			return e
		}
		attempt, e := p.Store.ReadAttempt(r.Operation.LastAttemptID)
		if e != nil {
			return e
		}
		if attempt.Generation != r.Generation || attempt.OperationID != r.Operation.ID || attempt.Kind != "create" || attempt.Phase != "client_reaped" || attempt.Outcome != "normal_exit" || attempt.ExitCode == nil || *attempt.ExitCode != 0 {
			return compat.Fail("E_OPERATION_UNCERTAIN", "create attempt not known to have finished successfully")
		}
		r.SandboxID = item.ID
		r.Compatibility.TemplateIdentity = item.Template
		if e = p.Store.Save(r); e != nil {
			return e
		}
	} else {
		if r.Operation != nil && r.Operation.LastAttemptID != "" {
			return p.markPriorCreateUncertain(&r)
		}
		if e = p.Backend.ShellSettings(ctx, p.Profile); e != nil {
			return e
		}
		if r.Operation == nil {
			if e = p.operation(&r, "create", "create"); e != nil {
				return e
			}
		}
		_, e = p.controlAttempt(ctx, &r, "create", sbx.CreateArgs(r.SandboxName, p.Config.Spec), sbx.CreateEnv(p.Config.Spec), 10*time.Minute)
		if e != nil {
			return p.MarkUncertain(&r, errors.Join(compat.Fail("E_CREATE_UNCERTAIN", "create outcome uncertain for machine key %s", r.MachineKey), e))
		}
		item, found, e = p.observe(ctx, r)
		if e != nil {
			return p.MarkUncertain(&r, e)
		}
		if !found {
			return p.MarkUncertain(&r, compat.Fail("E_CREATE_UNCERTAIN", "create returned but exact owned sandbox is absent from inventory"))
		}
		r.SandboxID = item.ID
		r.Compatibility.TemplateIdentity = item.Template
		if e = p.Store.Save(r); e != nil {
			return e
		}
	}
	if r.SandboxID == "" {
		return compat.Fail("E_OWNERSHIP_MISMATCH", "backend identity not established")
	}
	r.Operation.Step = "run"
	r.Phase = "starting"
	if e = p.Store.Save(r); e != nil {
		return e
	}
	return p.runReady(ctx, &r)
}

func (p Provider) markPriorCreateUncertain(r *state.Record) error {
	return p.MarkUncertain(r, compat.Fail(
		"E_OPERATION_UNCERTAIN",
		"previous create submitted a request; backend settlement must be acknowledged",
	))
}
func (p Provider) runReady(ctx context.Context, r *state.Record) error {
	_, e := p.controlAttempt(ctx, r, "run", sbx.RunArgs(r.SandboxName), nil, 2*time.Minute)
	if e != nil {
		return p.MarkUncertain(r, errors.Join(compat.Fail("E_OPERATION_UNCERTAIN", "detached run outcome for operation %s not settled", r.Operation.ID), e))
	}
	if e = p.readiness(ctx, r); e != nil {
		return p.markFailed(r, e)
	}
	return p.successful(r, "ready", "running")
}
func (p Provider) readiness(ctx context.Context, r *state.Record) error {
	if e := p.Profile.RequireDiskSizingQualification(); e != nil {
		return e
	}
	for _, check := range probe.Checks(r.Spec) {
		out, e := p.controlAttempt(ctx, r, "probe", sbx.ExecArgs(r.SandboxName, check.Program), nil, 20*time.Second)
		if e != nil {
			return errors.Join(compat.Fail("E_GUEST_CAPABILITY", "guest capability probe failed"), e)
		}
		if e = probe.Match(check, out); e != nil {
			return compat.Wrap("E_GUEST_CAPABILITY", e)
		}
	}
	item, e := p.ensureIdentity(ctx, *r)
	if e != nil {
		return e
	}
	if p.Profile.InventoryStates[item.State] != "running" || !item.Detached {
		return compat.Fail("E_SBX_STATE_UNKNOWN", "guest is not running in detached mode")
	}
	return nil
}
