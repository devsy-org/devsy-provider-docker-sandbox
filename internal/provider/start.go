package provider

import (
	"context"
	"errors"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func (p Provider) Start(ctx context.Context) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, e := p.live(ctx, true); e != nil {
		return e
	}
	if e := p.Profile.RequireDiskSizingQualification(); e != nil {
		return e
	}
	mutation, e := p.mutations(ctx)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(mutation)) }()
	r, e := p.load()
	if e != nil {
		return e
	}
	if e = p.guardPrior(r); e != nil {
		return e
	}
	if e = p.checkSpec(r); e != nil {
		return e
	}
	return p.startLocked(ctx, &r)
}

func (p Provider) readinessWithSession(ctx context.Context, r *state.Record) (result error) {
	sessions, e := p.Store.Acquire(ctx, "sessions", true)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	return p.readiness(ctx, r)
}

func (p Provider) startLocked(ctx context.Context, r *state.Record) (result error) {
	if r.SandboxID == "" {
		return compat.Fail("E_STATE_MISSING", "machine backend ID has never been recorded")
	}
	item, e := p.ensureIdentity(ctx, *r)
	if e != nil {
		return e
	}
	if r.Phase == phaseReady && r.DesiredState == stateRunning && r.Operation == nil &&
		p.Profile.InventoryStates[item.State] == stateRunning {
		return p.readinessWithSession(ctx, r)
	}
	sessions, e := p.begin(ctx, r, transition{phaseStarting, stateRunning, "start", stepRun})
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	return p.startAfterBegin(ctx, r)
}

func (p Provider) startAfterBegin(ctx context.Context, r *state.Record) error {
	item, e := p.ensureIdentity(ctx, *r)
	if e != nil {
		return p.failWithStep(r, e)
	}
	if p.Profile.InventoryStates[item.State] == stateRunning {
		if e = p.readiness(ctx, r); e != nil {
			return p.failWithStep(r, e)
		}
		return p.successful(r, phaseReady, stateRunning)
	}
	return p.runReady(ctx, r)
}
