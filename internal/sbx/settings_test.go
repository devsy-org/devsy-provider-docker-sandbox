package sbx

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func TestCheckShellSetting(t *testing.T) {
	measuredShellSetting := string(readMeasuredFixture(t, "settings-shell-full.json"))
	for _, test := range []struct {
		name, json, code string
	}{
		{"measured default", measuredShellSetting, ""},
		{"nonempty default", `{"default":"bash","description":"shell shortcut","key":"kit.shortcuts.shell","source":"default","type":"string","value":""}`, "E_SBX_SCHEMA"},
		{"effective override", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"user","type":"string","value":"/bin/bash"}`, "E_SHELL_OVERRIDE"},
		{"nonempty effective default", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"default","type":"string","value":"bash"}`, "E_SHELL_OVERRIDE"},
		{"unknown source", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"system","type":"string","value":""}`, "E_SHELL_OVERRIDE"},
		{"wrong key", `{"default":"","description":"shell shortcut","key":"other","source":"default","type":"string","value":""}`, "E_SBX_SCHEMA"},
		{"wrong type", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"default","type":"boolean","value":""}`, "E_SBX_SCHEMA"},
		{"missing source", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","type":"string","value":""}`, "E_SBX_SCHEMA"},
		{"missing default", `{"description":"shell shortcut","key":"kit.shortcuts.shell","source":"default","type":"string","value":""}`, "E_SBX_SCHEMA"},
		{"missing description", `{"default":"","key":"kit.shortcuts.shell","source":"default","type":"string","value":""}`, "E_SBX_SCHEMA"},
		{"extra field", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"default","type":"string","value":"","pending":true}`, "E_SBX_SCHEMA"},
		{"duplicate field", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","key":"kit.shortcuts.shell","source":"default","type":"string","value":""}`, "E_SBX_SCHEMA"},
		{"null value", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"default","type":"string","value":null}`, "E_SBX_SCHEMA"},
		{"malformed", `{"default":""`, "E_SBX_SCHEMA"},
		{"array", `["default"]`, "E_SBX_SCHEMA"},
		{"trailing", measuredShellSetting + ` false`, "E_SBX_SCHEMA"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkShellSetting([]byte(test.json))
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var tagged *compat.Error
			if !errors.As(err, &tagged) || tagged.Code != test.code {
				t.Fatalf("wanted %s, got %v", test.code, err)
			}
		})
	}
}

func TestShellSettingsReadOnly(t *testing.T) {
	client := testClient(t)
	profile := compat.Profile{ShellSettingSchema: compat.ShellSettingSchemaV0501}
	if err := client.ShellSettings(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL_SETTING_JSON", `{"default":"","description":"shell shortcut","key":"kit.shortcuts.shell","source":"user","type":"string","value":"/bin/bash"}`)
	var tagged *compat.Error
	if err := client.ShellSettings(context.Background(), profile); !errors.As(err, &tagged) || tagged.Code != "E_SHELL_OVERRIDE" {
		t.Fatalf("expected effective override rejection, got %v", err)
	}
	t.Setenv("SHELL_SETTING_JSON", "")
	t.Setenv("SHELL_SETTING_FAIL", "1")
	if err := client.ShellSettings(context.Background(), profile); !errors.As(err, &tagged) || tagged.Code != "E_SBX_UNAVAILABLE" {
		t.Fatalf("expected settings command failure, got %v", err)
	}
}

func TestShellSettingsRequiresProfileBeforeReading(t *testing.T) {
	client := Client{Path: "missing-sbx"}
	if err := client.ShellSettings(context.Background(), compat.Profile{}); err == nil {
		t.Fatal("unqualified settings unexpectedly accepted")
	}
}
