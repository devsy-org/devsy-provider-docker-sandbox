package provider

import (
	"context"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func Init(ctx context.Context, c config.Config, p compat.Profile) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	root, e := state.Root()
	if e != nil {
		return e
	}
	backend := sbx.Client{Path: c.SBXPath, Store: state.Store{Root: root}}
	provider := Provider{Config: c, Profile: p, Backend: backend, Warner: compat.Stderr}
	provider, e = provider.live(ctx, true)
	if e != nil {
		return e
	}
	if e = p.RequireDiskSizingQualification(); e != nil {
		return e
	}
	return backend.ShellSettings(ctx, p, provider.lenient)
}
