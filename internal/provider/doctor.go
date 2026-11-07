package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

const (
	compatibilityQualified = "qualified"
	compatibilityWarning   = "warning"
)

type Diagnosis struct {
	SchemaVersion      int             `json:"schema_version"`
	MachineKey         string          `json:"machine_key"`
	Compatibility      compat.Profile  `json:"compatibility"`
	Record             *state.Record   `json:"record,omitempty"`
	Attempts           []state.Attempt `json:"attempts,omitempty"`
	Backend            *sbx.Sandbox    `json:"backend,omitempty"`
	BackendError       string          `json:"backend_error,omitempty"`
	Warnings           []string        `json:"warnings,omitempty"`
	SBX                *compat.Tuple   `json:"sbx,omitempty"`
	CompatibilityState string          `json:"compatibility_state,omitempty"`
}

func hostWarnings(profile compat.Profile) []string {
	if warning := profile.HostWarning(); warning != "" {
		return []string{warning}
	}
	return nil
}

func joinBackendErrors(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}

func (p Provider) doctorVersion(ctx context.Context, out *Diagnosis) {
	v, e := p.Backend.Version(ctx)
	if e != nil {
		p.doctorBackendError(out, e)
		return
	}
	daemon, e := sbx.DaemonVersion(v, p.Profile)
	if e != nil {
		out.BackendError = e.Error()
		return
	}
	tuple := compat.Tuple{Client: v.Client.Version, Revision: v.Client.Revision, Daemon: daemon}
	out.SBX = &tuple
	out.CompatibilityState = compatibilityQualified
	if warning := p.Profile.TupleWarning(tuple); warning != "" {
		out.CompatibilityState = compatibilityWarning
		out.Warnings = append(out.Warnings, warning)
	}
}

func (p Provider) Doctor(ctx context.Context) ([]byte, error) {
	out := Diagnosis{
		SchemaVersion: 1,
		MachineKey:    p.Store.Key,
		Compatibility: p.Profile,
		Warnings:      hostWarnings(p.Profile),
	}
	r, e := p.doctorRecord(&out)
	if e != nil {
		return nil, e
	}
	a, e := p.Store.Attempts()
	if e != nil {
		return nil, e
	}
	out.Attempts = a
	p.doctorBackend(ctx, &out, r)
	return json.MarshalIndent(out, "", "  ")
}

func (p Provider) doctorBackend(ctx context.Context, out *Diagnosis, r state.Record) {
	if p.Backend == nil || p.Config.SBXPath == "" {
		return
	}
	p.doctorVersion(ctx, out)
	if out.Record == nil {
		_, e := p.Backend.Inventory(ctx, p.Profile)
		if e != nil {
			p.doctorBackendError(out, e)
		}
		return
	}
	item, found, e := p.observeAllowingAttached(ctx, r)
	if e != nil {
		p.doctorBackendError(out, e)
	} else if found {
		out.Backend = &item
	}
}

func (p Provider) doctorBackendError(out *Diagnosis, err error) {
	out.BackendError = joinBackendErrors(out.BackendError, err.Error())
	var tagged *compat.Error
	if errors.As(err, &tagged) && tagged.Code == "E_SBX_DAEMON_RESTART" {
		out.CompatibilityState = compatibilityWarning
		out.Warnings = append(out.Warnings, err.Error())
	}
}

func (p Provider) doctorRecord(out *Diagnosis) (state.Record, error) {
	r, e := p.Store.Load()
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return r, e
	}
	if e == nil {
		out.Record = &r
	}
	return r, nil
}

func List(_ context.Context, p compat.Profile) ([]byte, error) {
	root, e := state.Root()
	if e != nil {
		return nil, e
	}
	store := state.Store{Root: root}
	keys, e := store.ListKeys()
	if e != nil {
		return nil, e
	}
	records := make([]state.Record, 0, len(keys))
	for _, key := range keys {
		s, e := state.Open(key)
		if e != nil {
			return nil, e
		}
		r, e := s.Load()
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		records = append(records, r)
	}
	return json.MarshalIndent(struct {
		SchemaVersion int            `json:"schema_version"`
		Compatibility compat.Profile `json:"compatibility"`
		Records       []state.Record `json:"records"`
		Warnings      []string       `json:"warnings,omitempty"`
	}{1, p, records, hostWarnings(p)}, "", "  ")
}
