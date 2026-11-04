package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"io"
	"os"
	"time"
)

type Backend interface {
	Version(context.Context) (sbx.Version, error)
	Inventory(context.Context, compat.Profile) (sbx.Inventory, error)
	ShellSettings(context.Context, compat.Profile) error
	Start(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) (*sbx.Process, error)
}
type Provider struct {
	Config   config.Config
	Profile  compat.Profile
	Backend  Backend
	Store    state.Store
	Identity config.MachineIdentity
}

func New(c config.Config, p compat.Profile) (Provider, error) {
	identity, e := c.RequireMachine()
	if e != nil {
		return Provider{}, e
	}
	store, e := state.Open(identity.Key)
	if e != nil {
		return Provider{}, e
	}
	return Provider{Config: c, Profile: p, Backend: sbx.Client{Path: c.SBXPath, Store: store}, Store: store, Identity: identity}, nil
}
func FromKey(c config.Config, p compat.Profile, key string) (Provider, error) {
	s, e := state.Open(key)
	if e != nil {
		return Provider{}, e
	}
	return Provider{Config: c, Profile: p, Backend: sbx.Client{Path: c.SBXPath, Store: s}, Store: s}, nil
}
func (p Provider) compat(ctx context.Context, needsDevsy bool) error {
	if needsDevsy {
		if e := p.Profile.Devsy(ctx, p.Config.Devsy); e != nil {
			return e
		}
	}
	if e := p.Profile.Host(); e != nil {
		return e
	}
	v, e := p.Backend.Version(ctx)
	if e != nil {
		return e
	}
	if e = p.Profile.SBX(v.Client.Version, v.Client.Revision); e != nil {
		return e
	}
	if _, e = p.Backend.Inventory(ctx, p.Profile); e != nil {
		return e
	}
	v, e = p.Backend.Version(ctx)
	if e != nil {
		return e
	}
	if e = p.Profile.SBX(v.Client.Version, v.Client.Revision); e != nil {
		return e
	}
	_, e = sbx.ServerVersion(v, p.Profile)
	return e
}
func (p Provider) load() (state.Record, error) {
	r, e := p.Store.Load()
	if errors.Is(e, os.ErrNotExist) {
		return r, compat.Fail("E_STATE_MISSING", "no provider ownership journal for key %s", p.Store.Key)
	}
	if e != nil {
		return r, compat.Wrap("E_STORAGE", e)
	}
	if p.Identity.Key != "" && (r.MachineFolder != p.Identity.Folder || r.MachineID != p.Identity.ID || r.Context != p.Identity.Context) {
		return r, compat.Fail("E_OWNERSHIP_MISMATCH", "Devsy machine identity conflicts with durable ownership")
	}
	return r, nil
}
func (p Provider) observe(ctx context.Context, r state.Record) (sbx.Sandbox, bool, error) {
	items, e := p.Backend.Inventory(ctx, p.Profile)
	if e != nil {
		return sbx.Sandbox{}, false, e
	}
	item, found := items.Find(r.SandboxName)
	if found && r.SandboxID != "" && item.ID != r.SandboxID {
		return sbx.Sandbox{}, false, compat.Fail("E_OWNERSHIP_MISMATCH", "sandbox name matches a different backend ID")
	}
	if found && len(item.Workspaces) > 0 {
		return sbx.Sandbox{}, false, compat.Fail("E_OWNERSHIP_MISMATCH", "owned sandbox unexpectedly contains host workspace mounts")
	}
	return item, found, nil
}
func (p Provider) unsettled(r state.Record) error {
	attempts, e := p.Store.Incomplete()
	if e != nil {
		return e
	}
	for _, a := range attempts {
		if a.Generation == r.Generation {
			return compat.Fail("E_UNCLEAN_ATTEMPT", "attempt %s (%s) requires exact acknowledgement", a.AttemptID, a.Kind)
		}
	}
	if r.Operation != nil && r.Operation.Phase == "uncertain" {
		return compat.Fail("E_OPERATION_UNCERTAIN", "operation %s requires backend settlement acknowledgement", r.Operation.ID)
	}
	return nil
}
func (p Provider) checkSpec(r state.Record) error {
	if r.Spec != p.Config.Spec {
		return compat.Fail("E_CONFIG_DRIFT", "existing machine resources differ; create a separate machine and transfer data explicitly")
	}
	return nil
}
func (p Provider) operation(r *state.Record, kind, step string) error {
	id, e := state.Token()
	if e != nil {
		return compat.Wrap("E_STORAGE", e)
	}
	r.Operation = &state.Operation{ID: id, Kind: kind, Phase: "active", Step: step}
	return p.Store.Save(*r)
}
func (p Provider) markFailed(r *state.Record, e error) error {
	r.Phase = "failed"
	r.LastErrorCode = errorCode(e)
	if r.Operation != nil && r.Operation.Phase == "active" {
		r.Operation = nil
	}
	saveErr := p.Store.Save(*r)
	if saveErr != nil {
		return errors.Join(e, saveErr)
	}
	return e
}
func errorCode(err error) string {
	var e *compat.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "E_SBX_UNAVAILABLE"
}
func (p Provider) begin(ctx context.Context, r *state.Record, phase, desired, kind, step string) (*state.Lock, error) {
	admission, e := p.Store.Acquire(ctx, "admission", false)
	if e != nil {
		return nil, e
	}
	if r.Operation != nil && r.Operation.Kind == "create" && r.Operation.LastAttemptID != "" && r.SandboxID == "" {
		return nil, errors.Join(compat.Fail("E_OPERATION_UNCERTAIN", "prior create requires reconciliation"), closeLock(admission))
	}
	drainCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sessions, e := p.Store.Acquire(drainCtx, "sessions", false)
	if e != nil {
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), closeLock(admission))
		}
		return nil, errors.Join(compat.Fail("E_ACTIVE_SESSIONS", "active sessions did not drain"), closeLock(admission))
	}
	if e = p.unsettled(*r); e != nil {
		return nil, errors.Join(e, closeLock(sessions), closeLock(admission))
	}
	r.Epoch++
	r.Phase = phase
	r.DesiredState = desired
	if e = p.operation(r, kind, step); e != nil {
		return nil, errors.Join(e, closeLock(sessions), closeLock(admission))
	}
	if e = closeLock(admission); e != nil {
		return nil, errors.Join(e, closeLock(sessions))
	}
	return sessions, nil
}
func (p Provider) live(ctx context.Context, needsDevsy bool) error { return p.compat(ctx, needsDevsy) }
func (p Provider) guardPrior(r state.Record) (result error) {
	attempts, err := p.Store.Incomplete()
	if err != nil {
		return err
	}
	for _, a := range attempts {
		if a.Generation == r.Generation && a.Kind != "command" && a.Kind != "probe" {
			return compat.Fail("E_UNCLEAN_ATTEMPT", "control attempt %s requires exact acknowledgement", a.AttemptID)
		}
	}
	if r.Operation != nil && r.Operation.Phase == "uncertain" {
		return compat.Fail("E_OPERATION_UNCERTAIN", "operation %s requires backend settlement acknowledgement", r.Operation.ID)
	}
	sessions, err := p.Store.TryAcquire("sessions", false)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return compat.Wrap("E_STORAGE", err)
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	return p.unsettled(r)
}
func (p Provider) failWithStep(r *state.Record, err error) error {
	if r.Operation != nil && r.Operation.Phase == "uncertain" {
		return errors.Join(err, p.Store.Save(*r))
	}
	return p.markFailed(r, err)
}
func waitState(ctx context.Context, interval time.Duration, observe func() (bool, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		ok, e := observe()
		if e != nil {
			return e
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (p Provider) successful(r *state.Record, phase, desired string) error {
	r.Operation = nil
	r.Phase = phase
	r.DesiredState = desired
	r.LastErrorCode = ""
	return p.Store.Save(*r)
}
func (p Provider) ensureIdentity(ctx context.Context, r state.Record) (sbx.Sandbox, error) {
	s, found, e := p.observe(ctx, r)
	if e != nil {
		return s, e
	}
	if !found {
		return s, compat.Fail("E_MACHINE_MISSING", "recorded backend ID %q unavailable; guest data cannot be recreated automatically", r.SandboxID)
	}
	if r.SandboxID == "" {
		return s, compat.Fail("E_OWNERSHIP_MISMATCH", "backend identity was not journaled")
	}
	return s, nil
}
func closeLock(lock *state.Lock) error {
	return compat.Wrap("E_STORAGE", lock.Close())
}
func (p Provider) mutations(ctx context.Context) (*state.Lock, error) {
	lock, e := p.Store.Acquire(ctx, "mutation", false)
	if e != nil {
		return nil, compat.Wrap("E_STORAGE", e)
	}
	return lock, nil
}
func (p Provider) quotedError(ctx context.Context, kind string, e error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return errors.Join(compat.Fail("E_TIMEOUT", "sbx %s did not finish before its deadline", kind), context.DeadlineExceeded)
	}
	if errors.Is(e, context.Canceled) {
		return e
	}
	return compat.Fail("E_SBX_UNAVAILABLE", "sbx %s failed: %v", kind, e)
}
