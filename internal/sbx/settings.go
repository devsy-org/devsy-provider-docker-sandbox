package sbx

import (
	"context"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func checkShellSetting(data []byte) error {
	fields, err := stringFields(data, "default", "description", "key", "source", "type", "value")
	if err != nil {
		return compat.Fail("E_SBX_SCHEMA", "invalid effective shell setting JSON: %v", err)
	}
	if fields["key"] != "kit.shortcuts.shell" || fields["type"] != "string" || fields["default"] != "" {
		return compat.Fail("E_SBX_SCHEMA", "unrecognized effective shell setting schema")
	}
	if fields["source"] != "default" || fields["value"] != "" {
		return compat.Fail("E_SHELL_OVERRIDE", "effective shell shortcut setting is not the qualified empty default")
	}
	return nil
}

func (c Client) ShellSettings(ctx context.Context, p compat.Profile) error {
	if e := p.RequireShellSchema(); e != nil {
		return e
	}
	data, e := c.Control(ctx, 10*time.Second, []string{"settings", "get", "--json", "kit.shortcuts.shell"}, nil)
	if e != nil {
		return compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	return checkShellSetting(data)
}
