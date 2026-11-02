package sbx

import (
	"context"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func (c Client) ShellSettings(ctx context.Context, p compat.Profile) error {
	if e := p.RequireShellSchema(); e != nil {
		return e
	}
	return compat.Fail("E_SHELL_OVERRIDE", "qualified effective shell settings inspection is not implemented for profile %q", p.ShellSettingSchema)
}
