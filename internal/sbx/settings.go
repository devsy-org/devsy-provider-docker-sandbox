package sbx

import (
	"context"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

const shellDefault = "default"

func shellSettingFields(lenient bool) []string {
	if lenient {
		return []string{shellDefault, "key", "source", "type", "value"}
	}
	return []string{shellDefault, "description", "key", "source", "type", "value"}
}

func checkShellSetting(data []byte, lenient bool) error {
	fields, err := stringFields(data, lenient, shellSettingFields(lenient)...)
	if err != nil {
		return compat.Fail("E_SBX_SCHEMA", "invalid effective shell setting JSON: %v", err)
	}
	if fields["key"] != "kit.shortcuts.shell" || fields["type"] != "string" ||
		fields[shellDefault] != "" {
		return compat.Fail("E_SBX_SCHEMA", "unrecognized effective shell setting schema")
	}
	if fields["source"] != shellDefault || fields["value"] != "" {
		return compat.Fail(
			"E_SHELL_OVERRIDE",
			"effective shell shortcut setting is not the qualified empty default",
		)
	}
	return nil
}

func (c Client) ShellSettings(ctx context.Context, p compat.Profile, lenient bool) error {
	if e := p.RequireShellSchema(); e != nil {
		return e
	}
	data, e := c.Control(
		ctx,
		10*time.Second,
		[]string{"settings", "get", "--json", "kit.shortcuts.shell"},
		nil,
	)
	if e != nil {
		return compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	return checkShellSetting(data, lenient)
}
