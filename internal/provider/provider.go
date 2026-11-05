package provider

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

type Backend interface {
	Version(context.Context) (sbx.Version, error)
	Inventory(context.Context, compat.Profile) (sbx.Inventory, error)
	Inspect(context.Context, compat.Profile, string) (sbx.Inspection, error)
	ShellSettings(context.Context, compat.Profile, bool) error
	Start(context.Context, sbx.StartRequest) (*sbx.Process, error)
}
type Provider struct {
	Config   config.Config
	Profile  compat.Profile
	Backend  Backend
	Store    state.Store
	Identity config.MachineIdentity
	Warner   *compat.Warner
	lenient  bool
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
	return Provider{
		Config:   c,
		Profile:  p,
		Backend:  sbx.Client{Path: c.SBXPath, Store: store},
		Store:    store,
		Identity: identity,
		Warner:   compat.Stderr,
	}, nil
}

func FromKey(c config.Config, p compat.Profile, key string) (Provider, error) {
	s, e := state.Open(key)
	if e != nil {
		return Provider{}, e
	}
	return Provider{
		Config:  c,
		Profile: p,
		Backend: sbx.Client{Path: c.SBXPath, Store: s},
		Store:   s,
		Warner:  compat.Stderr,
	}, nil
}

func (p Provider) compat(ctx context.Context, needsDevsy bool) (bool, error) {
	if needsDevsy {
		if e := p.Profile.Devsy(ctx, p.Config.Devsy); e != nil {
			return false, e
		}
	}
	if e := p.Profile.Host(); e != nil {
		return false, e
	}
	if _, e := p.Backend.Version(ctx); e != nil {
		return false, e
	}
	if _, e := p.Backend.Inventory(ctx, p.Profile); e != nil {
		return false, e
	}
	v, e := p.Backend.Version(ctx)
	if e != nil {
		return false, e
	}
	daemon, e := sbx.DaemonVersion(v, p.Profile)
	if e != nil {
		return false, e
	}
	tuple := compat.Tuple{Client: v.Client.Version, Revision: v.Client.Revision, Daemon: daemon}
	warning := p.Profile.TupleWarning(tuple)
	p.Warner.Warn(warning)
	return warning != "", nil
}

func (p Provider) load() (state.Record, error) {
	r, e := p.Store.Load()
	if errors.Is(e, os.ErrNotExist) {
		return r, compat.Fail(
			"E_STATE_MISSING",
			"no provider ownership journal for key %s",
			p.Store.Key,
		)
	}
	if e != nil {
		return r, compat.Wrap("E_STORAGE", e)
	}
	if p.Identity.Key != "" &&
		(r.MachineFolder != p.Identity.Folder || r.MachineID != p.Identity.ID || r.Context != p.Identity.Context) {
		return r, compat.Fail(
			codeOwnershipMismatch,
			"Devsy machine identity conflicts with durable ownership",
		)
	}
	return r, nil
}

func (p Provider) observe(ctx context.Context, r state.Record) (sbx.Sandbox, bool, error) {
	return p.observeWithRetry(ctx, r, false)
}

func (p Provider) observeAllowingAttached(
	ctx context.Context,
	r state.Record,
) (sbx.Sandbox, bool, error) {
	return p.observeWithRetry(ctx, r, true)
}

func (p Provider) observeWithRetry(
	ctx context.Context,
	r state.Record,
	allowAttached bool,
) (sbx.Sandbox, bool, error) {
	item, found, err := p.observeSnapshot(ctx, r, allowAttached)
	if err == nil || ctx.Err() != nil {
		return item, found, err
	}
	var qualified *compat.Error
	if !errors.As(err, &qualified) ||
		(qualified.Code != "E_SBX_STATE_UNKNOWN" && qualified.Code != "E_SBX_UNAVAILABLE") {
		return item, found, err
	}
	return p.observeSnapshot(ctx, r, allowAttached)
}

func (p Provider) observeSnapshot(
	ctx context.Context,
	r state.Record,
	allowAttached bool,
) (sbx.Sandbox, bool, error) {
	items, e := p.Backend.Inventory(ctx, p.Profile)
	if e != nil {
		return sbx.Sandbox{}, false, e
	}
	item, found := items.Find(r.SandboxName)
	if !found {
		return sbx.Sandbox{}, false, nil
	}
	if e = p.checkInventory(item, r, allowAttached); e != nil {
		return sbx.Sandbox{}, false, e
	}
	inspected, e := p.Backend.Inspect(ctx, p.Profile, item.Name)
	if e != nil {
		return sbx.Sandbox{}, false, e
	}
	if e = checkInspection(item, inspected, r); e != nil {
		return sbx.Sandbox{}, false, e
	}
	item.Template = inspected.ImageDigest
	return item, true, nil
}

func (p Provider) checkInventory(item sbx.Sandbox, r state.Record, allowAttached bool) error {
	kind, e := p.inventoryKind(item.State)
	if e != nil {
		return e
	}
	if kind == stateRunning && !item.Detached && !allowAttached {
		return compat.Fail(codeSBXSchema, "owned running sandbox is not detached")
	}
	if r.SandboxID != "" && item.ID != r.SandboxID {
		return compat.Fail(codeOwnershipMismatch, "sandbox name matches a different backend ID")
	}
	if len(item.Workspaces) != 0 {
		return compat.Fail(
			codeOwnershipMismatch,
			"owned sandbox unexpectedly contains host workspace mounts",
		)
	}
	return nil
}

func (p Provider) inventoryKind(backendState string) (string, error) {
	kind, qualified := p.Profile.InventoryStates[backendState]
	if !qualified || !qualifiedInventoryKind(kind) {
		return "", compat.Fail(
			"E_SBX_STATE_UNKNOWN",
			"unqualified owned sandbox state %q",
			backendState,
		)
	}
	return kind, nil
}

func checkInspection(item sbx.Sandbox, inspected sbx.Inspection, r state.Record) error {
	if inspected.Name != item.Name || (inspected.ID != "" && inspected.ID != item.ID) {
		return compat.Fail(codeOwnershipMismatch, "sbx inspect identity disagrees with inventory")
	}
	if inspected.State != item.State {
		return compat.Fail("E_SBX_STATE_UNKNOWN", "sbx inspect state disagrees with inventory")
	}
	if inspected.ImageDigest == "" || inspected.RuntimeMounts == nil {
		return compat.Fail(codeSBXSchema, "incomplete sbx inspect image digest or runtime mounts")
	}
	if unexpectedInspectionMounts(inspected, r) {
		return compat.Fail(
			codeOwnershipMismatch,
			"owned sandbox has unexpected mounts or image digest",
		)
	}
	return nil
}

func qualifiedInventoryKind(kind string) bool {
	switch kind {
	case stateRunning, stateStopped, stateTransitional, phaseFailed:
		return true
	default:
		return false
	}
}

func unexpectedInspectionMounts(inspected sbx.Inspection, r state.Record) bool {
	return len(inspected.RuntimeMounts) != 0 ||
		(r.Compatibility.TemplateIdentity != "" && inspected.ImageDigest != r.Compatibility.TemplateIdentity)
}

func (p Provider) unsettled(r state.Record) error {
	attempts, e := p.Store.Incomplete()
	if e != nil {
		return e
	}
	for _, a := range attempts {
		if a.Generation == r.Generation {
			return compat.Fail(
				codeUncleanAttempt,
				"attempt %s (%s) requires exact acknowledgement",
				a.AttemptID,
				a.Kind,
			)
		}
	}
	if r.Operation != nil && r.Operation.Phase == phaseUncertain {
		return compat.Fail(
			codeOperationUncertain,
			"operation %s requires backend settlement acknowledgement",
			r.Operation.ID,
		)
	}
	return nil
}

func (p Provider) checkSpec(r state.Record) error {
	if r.Spec != p.Config.Spec {
		return compat.Fail(
			"E_CONFIG_DRIFT",
			"existing machine resources differ; create a separate machine and transfer data explicitly",
		)
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
	r.Phase = phaseFailed
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
	if e, ok := errors.AsType[*compat.Error](err); ok {
		return e.Code
	}
	return "E_SBX_UNAVAILABLE"
}

type transition struct{ phase, desired, kind, step string }

func (p Provider) begin(
	ctx context.Context,
	r *state.Record,
	change transition,
) (*state.Lock, error) {
	admission, e := p.Store.Acquire(ctx, "admission", false)
	if e != nil {
		return nil, e
	}
	if e := guardPendingCreate(r); e != nil {
		return nil, errors.Join(e, closeLock(admission))
	}
	sessions, e := p.drainSessions(ctx, admission)
	if e != nil {
		return nil, e
	}
	if e = p.unsettled(*r); e != nil {
		return nil, errors.Join(e, closeLock(sessions), closeLock(admission))
	}
	r.Epoch++
	r.Phase = change.phase
	r.DesiredState = change.desired
	if e = p.operation(r, change.kind, change.step); e != nil {
		return nil, errors.Join(e, closeLock(sessions), closeLock(admission))
	}
	if e = closeLock(admission); e != nil {
		return nil, errors.Join(e, closeLock(sessions))
	}
	return sessions, nil
}

func guardPendingCreate(r *state.Record) error {
	if r.Operation != nil && r.Operation.Kind == kindCreate && r.Operation.LastAttemptID != "" &&
		r.SandboxID == "" {
		return compat.Fail(codeOperationUncertain, "prior create requires reconciliation")
	}
	return nil
}

func (p Provider) drainSessions(ctx context.Context, admission *state.Lock) (*state.Lock, error) {
	drainCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sessions, e := p.Store.Acquire(drainCtx, "sessions", false)
	if e != nil {
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), closeLock(admission))
		}
		return nil, errors.Join(
			compat.Fail("E_ACTIVE_SESSIONS", "active sessions did not drain"),
			closeLock(admission),
		)
	}
	return sessions, nil
}

func (p Provider) live(
	ctx context.Context,
	needsDevsy bool,
) (Provider, error) {
	lenient, e := p.compat(ctx, needsDevsy)
	bound := p
	bound.lenient = lenient
	return bound, e
}

func (p Provider) guardPrior(r state.Record) (result error) {
	if err := p.guardControlAttempts(r); err != nil {
		return err
	}
	if r.Operation != nil && r.Operation.Phase == phaseUncertain {
		return compat.Fail(
			codeOperationUncertain,
			"operation %s requires backend settlement acknowledgement",
			r.Operation.ID,
		)
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

func (p Provider) guardControlAttempts(r state.Record) error {
	attempts, err := p.Store.Incomplete()
	if err != nil {
		return err
	}
	for _, a := range attempts {
		if a.Generation == r.Generation && a.Kind != kindCommand && a.Kind != kindProbe {
			return compat.Fail(
				codeUncleanAttempt,
				"control attempt %s requires exact acknowledgement",
				a.AttemptID,
			)
		}
	}
	return nil
}

func (p Provider) failWithStep(r *state.Record, err error) error {
	if r.Operation != nil && r.Operation.Phase == phaseUncertain {
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
		return s, compat.Fail(
			"E_MACHINE_MISSING",
			"recorded backend ID %q unavailable; guest data cannot be recreated automatically",
			r.SandboxID,
		)
	}
	if r.SandboxID == "" {
		return s, compat.Fail(codeOwnershipMismatch, "backend identity was not journaled")
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
		return errors.Join(
			compat.Fail("E_TIMEOUT", "sbx %s did not finish before its deadline", kind),
			context.DeadlineExceeded,
		)
	}
	if errors.Is(e, context.Canceled) {
		return e
	}
	return compat.Fail("E_SBX_UNAVAILABLE", "sbx %s failed: %v", kind, e)
}
