package provider

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/probe"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func (p Provider) Create(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	bound, e := p.live(ctx, true)
	if e != nil {
		return e
	}
	return bound.create(ctx)
}

func (p Provider) create(ctx context.Context) (result error) {
	creationCompatibility, e := p.createCompatibility(ctx)
	if e != nil {
		return e
	}
	mutation, e := p.mutations(ctx)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(mutation)) }()
	r, complete, e := p.prepareCreateRecord(ctx, creationCompatibility)
	if e != nil || complete {
		return e
	}
	return p.completeCreate(ctx, &r)
}

func (p Provider) createCompatibility(ctx context.Context) (state.Compatibility, error) {
	if e := p.Profile.RequireDiskSizingQualification(); e != nil {
		return state.Compatibility{}, e
	}
	current, e := p.Backend.Version(ctx)
	if e != nil {
		return state.Compatibility{}, e
	}
	daemon, e := sbx.DaemonVersion(current, p.Profile)
	if e != nil {
		return state.Compatibility{}, e
	}
	return state.Compatibility{
		SBXVersion:       current.Client.Version,
		SBXRevision:      current.Client.Revision,
		SBXDaemonVersion: daemon,
	}, nil
}

func (p Provider) completeCreate(ctx context.Context, r *state.Record) error {
	if r.Operation != nil && r.Operation.Phase == phaseUncertain {
		return compat.Fail(
			codeOperationUncertain,
			"pending create %s requires backend acknowledgement",
			r.Operation.ID,
		)
	}
	if e := p.establishCreateOwnership(ctx, r); e != nil {
		return e
	}
	if r.SandboxID == "" {
		return compat.Fail(codeOwnershipMismatch, "backend identity not established")
	}
	r.Operation.Step = stepRun
	r.Phase = phaseStarting
	if e := p.Store.Save(*r); e != nil {
		return e
	}
	return p.runReady(ctx, r)
}

func (p Provider) prepareCreateRecord(
	ctx context.Context,
	creationCompatibility state.Compatibility,
) (state.Record, bool, error) {
	r, e := p.load()
	if e != nil {
		return p.prepareNewCreateRecord(r, e, creationCompatibility)
	}
	return p.prepareExistingCreateRecord(ctx, r, creationCompatibility)
}

func (p Provider) prepareNewCreateRecord(
	r state.Record,
	loadErr error,
	creationCompatibility state.Compatibility,
) (state.Record, bool, error) {
	if !errors.Is(loadErr, os.ErrNotExist) && errorCode(loadErr) != "E_STATE_MISSING" {
		return r, false, loadErr
	}
	r, e := state.New(p.Identity, p.Config.Spec)
	if e != nil {
		return r, false, compat.Wrap("E_STORAGE", e)
	}
	r.Compatibility = creationCompatibility
	return r, false, p.Store.Save(r)
}

func (p Provider) prepareExistingCreateRecord(
	ctx context.Context,
	r state.Record,
	creationCompatibility state.Compatibility,
) (state.Record, bool, error) {
	if e := p.guardPrior(r); e != nil {
		return r, false, e
	}
	if r.Phase == phaseDeleted && r.Operation == nil {
		next, e := p.prepareDeletedCreate(r, creationCompatibility)
		if e != nil {
			return next, false, e
		}
		r = next
	} else if e := p.checkSpec(r); e != nil {
		return r, false, e
	}
	if r.SandboxID != "" {
		return p.resumeExistingCreate(ctx, r)
	}
	return r, false, nil
}

func (p Provider) prepareDeletedCreate(
	r state.Record,
	creationCompatibility state.Compatibility,
) (state.Record, error) {
	if e := p.Store.Archive(r); e != nil {
		return r, e
	}
	next, e := state.New(p.Identity, p.Config.Spec)
	if e != nil {
		return next, compat.Wrap("E_STORAGE", e)
	}
	next.Compatibility = creationCompatibility
	return next, p.Store.Save(next)
}

func (p Provider) resumeExistingCreate(
	ctx context.Context,
	r state.Record,
) (state.Record, bool, error) {
	if r.Phase == phaseReady || r.Phase == stateStopped || r.Phase == phaseFailed {
		return r, true, p.startLocked(ctx, &r)
	}
	return r, true, compat.Fail(
		codeOperationUncertain,
		"existing machine generation %s cannot be recreated",
		r.Generation,
	)
}

func (p Provider) establishCreateOwnership(ctx context.Context, r *state.Record) error {
	item, found, e := p.observeAllowingAttached(ctx, *r)
	if e != nil {
		return e
	}
	if found {
		return p.acceptExistingCreate(r, item)
	}
	return p.submitCreate(ctx, r)
}

func (p Provider) acceptExistingCreate(r *state.Record, item sbx.Sandbox) error {
	if r.Operation == nil || r.Operation.Kind != kindCreate || r.Operation.LastAttemptID == "" {
		return compat.Fail(
			codeOwnershipMismatch,
			"backend name exists without established creation provenance",
		)
	}
	if e := p.requireCreateAttempt(*r); e != nil {
		return e
	}
	attempt, e := p.Store.ReadAttempt(r.Operation.LastAttemptID)
	if e != nil {
		return e
	}
	if !successfulCreateAttempt(attempt, *r) {
		return compat.Fail(
			codeOperationUncertain,
			"create attempt not known to have finished successfully",
		)
	}
	r.SandboxID = item.ID
	r.Compatibility.TemplateIdentity = item.Template
	return p.Store.Save(*r)
}

func successfulCreateAttempt(attempt state.Attempt, r state.Record) bool {
	return attempt.Generation == r.Generation && attempt.OperationID == r.Operation.ID &&
		attempt.Kind == kindCreate && attempt.Phase == phaseClientReaped &&
		attempt.Outcome == outcomeNormalExit && attempt.ExitCode != nil && *attempt.ExitCode == 0
}

func (p Provider) submitCreate(ctx context.Context, r *state.Record) error {
	if e := p.prepareCreateSubmission(ctx, r); e != nil {
		return e
	}
	_, e := p.controlAttempt(ctx, r, controlRequest{
		kind: kindCreate, args: sbx.CreateArgs(r.SandboxName, p.Config.Spec),
		extra: sbx.CreateEnv(p.Config.Spec), timeout: 10 * time.Minute,
	})
	if e != nil {
		return p.MarkUncertain(
			r,
			errors.Join(
				compat.Fail(
					"E_CREATE_UNCERTAIN",
					"create outcome uncertain for machine key %s",
					r.MachineKey,
				),
				e,
			),
		)
	}
	return p.observeSubmittedCreate(ctx, r)
}

func (p Provider) prepareCreateSubmission(ctx context.Context, r *state.Record) error {
	if r.Operation != nil && r.Operation.LastAttemptID != "" {
		return p.markPriorCreateUncertain(r)
	}
	if e := p.Backend.ShellSettings(ctx, p.Profile, p.lenient); e != nil {
		return e
	}
	if r.Operation == nil {
		return p.operation(r, kindCreate, kindCreate)
	}
	return nil
}

func (p Provider) observeSubmittedCreate(ctx context.Context, r *state.Record) error {
	item, found, e := p.observeAllowingAttached(ctx, *r)
	if e != nil {
		return p.MarkUncertain(r, e)
	}
	if !found {
		return p.MarkUncertain(
			r,
			compat.Fail(
				"E_CREATE_UNCERTAIN",
				"create returned but exact owned sandbox is absent from inventory",
			),
		)
	}
	r.SandboxID = item.ID
	r.Compatibility.TemplateIdentity = item.Template
	return p.Store.Save(*r)
}

func (p Provider) markPriorCreateUncertain(r *state.Record) error {
	return p.MarkUncertain(r, compat.Fail(
		codeOperationUncertain,
		"previous create submitted a request; backend settlement must be acknowledged",
	))
}

func (p Provider) runReady(ctx context.Context, r *state.Record) error {
	_, e := p.controlAttempt(
		ctx,
		r,
		controlRequest{kind: stepRun, args: sbx.RunArgs(r.SandboxName), timeout: 2 * time.Minute},
	)
	if e != nil {
		return p.MarkUncertain(
			r,
			errors.Join(
				compat.Fail(
					codeOperationUncertain,
					"detached run outcome for operation %s not settled",
					r.Operation.ID,
				),
				e,
			),
		)
	}
	if e = p.readiness(ctx, r); e != nil {
		return p.markFailed(r, e)
	}
	return p.successful(r, phaseReady, stateRunning)
}

func (p Provider) readiness(ctx context.Context, r *state.Record) error {
	if e := p.Profile.RequireDiskSizingQualification(); e != nil {
		return e
	}
	for _, check := range probe.Checks(r.Spec) {
		out, e := p.controlAttempt(ctx, r, controlRequest{
			kind:    kindProbe,
			args:    sbx.ExecArgs(r.SandboxName, check.Program),
			timeout: 20 * time.Second,
		})
		if e != nil {
			return errors.Join(
				compat.Fail("E_GUEST_CAPABILITY", "guest capability probe failed"),
				e,
			)
		}
		if e = probe.Match(check, out); e != nil {
			return compat.Wrap("E_GUEST_CAPABILITY", e)
		}
	}
	item, e := p.ensureIdentity(ctx, *r)
	if e != nil {
		return e
	}
	if p.Profile.InventoryStates[item.State] != stateRunning || !item.Detached {
		return compat.Fail("E_SBX_STATE_UNKNOWN", "guest is not running in detached mode")
	}
	return nil
}
