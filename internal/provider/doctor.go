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

type Diagnosis struct {
	SchemaVersion int             `json:"schema_version"`
	MachineKey    string          `json:"machine_key"`
	Compatibility compat.Profile  `json:"compatibility"`
	Record        *state.Record   `json:"record,omitempty"`
	Attempts      []state.Attempt `json:"attempts,omitempty"`
	Backend       *sbx.Sandbox    `json:"backend,omitempty"`
	BackendError  string          `json:"backend_error,omitempty"`
}

func (p Provider) Doctor(ctx context.Context) ([]byte, error) {
	out := Diagnosis{SchemaVersion: 1, MachineKey: p.Store.Key, Compatibility: p.Profile}
	r, e := p.doctorRecord(&out)
	if e != nil {
		return nil, e
	}
	a, e := p.Store.Attempts()
	if e != nil {
		return nil, e
	}
	out.Attempts = a
	if out.Record != nil && p.Backend != nil && p.Config.SBXPath != "" {
		if item, found, e := p.observeAllowingAttached(ctx, r); e != nil {
			out.BackendError = e.Error()
		} else if found {
			out.Backend = &item
		}
	}
	return json.MarshalIndent(out, "", "  ")
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
	}{1, p, records}, "", "  ")
}
