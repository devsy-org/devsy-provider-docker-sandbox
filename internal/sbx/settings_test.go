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
		{"nonempty default", `{"default":"bash","description":"shell shortcut","key":"kit.short` +
			`cuts.shell","source":"default","type":"string","value":""}`, testSchema},
		{"effective override", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","source":"user","type":"string","value":"/bin/bash"}`, testShellOverride},
		{"nonempty effective default", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","source":"default","type":"string","value":"bash"}`, testShellOverride},
		{"unknown source", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","source":"system","type":"string","value":""}`, testShellOverride},
		{"wrong key", `{"default":"","description":"shell shortcut","key":"other","sourc` +
			`e":"default","type":"string","value":""}`, testSchema},
		{"wrong type", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","source":"default","type":"boolean","value":""}`, testSchema},
		{"missing source", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","type":"string","value":""}`, testSchema},
		{"missing default", `{"description":"shell shortcut","key":"kit.shortcuts.shell","sour` +
			`ce":"default","type":"string","value":""}`, testSchema},
		{"missing description", `{"default":"","key":"kit.shortcuts.shell","source":"default","typ` +
			`e":"string","value":""}`, testSchema},
		{"extra field", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","source":"default","type":"string","value":"","pending":t` +
			`rue}`, testSchema},
		{"duplicate field", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","key":"kit.shortcuts.shell","source":"default","type":"st` +
			`ring","value":""}`, testSchema},
		{"null value", `{"default":"","description":"shell shortcut","key":"kit.shortcuts` +
			`.shell","source":"default","type":"string","value":null}`, testSchema},
		{"malformed", `{"default":""`, testSchema},
		{"array", `["default"]`, testSchema},
		{"trailing", measuredShellSetting + ` false`, testSchema},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkShellSetting([]byte(test.json), false)
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
	profile := compat.Profile{ShellSettingSchema: compat.ShellSettingSchema}
	if err := client.ShellSettings(context.Background(), profile, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv(
		"SHELL_SETTING_JSON",
		`{"default":"","description":"shell shortcut","key":"kit.shortcuts`+
			`.shell","source":"user","type":"string","value":"/bin/bash"}`,
	)
	var tagged *compat.Error
	if err := client.ShellSettings(
		context.Background(),
		profile,
		false,
	); !errors.As(err, &tagged) ||
		tagged.Code != testShellOverride {
		t.Fatalf("expected effective override rejection, got %v", err)
	}
	t.Setenv("SHELL_SETTING_JSON", "")
	t.Setenv("SHELL_SETTING_FAIL", "1")
	if err := client.ShellSettings(
		context.Background(),
		profile,
		false,
	); !errors.As(err, &tagged) ||
		tagged.Code != "E_SBX_UNAVAILABLE" {
		t.Fatalf("expected settings command failure, got %v", err)
	}
}

func TestShellSettingsRequiresProfileBeforeReading(t *testing.T) {
	client := Client{Path: "missing-sbx"}
	if err := client.ShellSettings(context.Background(), compat.Profile{}, false); err == nil {
		t.Fatal("unqualified settings unexpectedly accepted")
	}
}

func TestCheckShellSettingLenient(t *testing.T) {
	const extra = `{"default":"","key":"kit.shortcuts.shell","source":"default","type":"string","value":"","pending":true}`
	if err := checkShellSetting([]byte(extra), true); err != nil {
		t.Fatal(err)
	}
	if err := checkShellSetting([]byte(extra), false); err == nil {
		t.Fatal("strict decoding accepted an incomplete or extended setting")
	}
	for _, test := range []struct{ json, code string }{
		{`{"default":"","key":"kit.shortcuts.shell","type":"string","value":""}`, testSchema},
		{`{"default":"","key":"kit.shortcuts.shell","source":"default","type":"string","value":null}`, testSchema},
		{`{"default":"","key":"kit.shortcuts.shell","source":"user","type":"string","value":"/bin/bash"}`, testShellOverride},
		{`["default"]`, testSchema},
		{`{"default":""`, testSchema},
	} {
		var tagged *compat.Error
		if err := checkShellSetting([]byte(test.json), true); !errors.As(err, &tagged) ||
			tagged.Code != test.code {
			t.Fatalf("lenient decoding of %s: %v", test.json, err)
		}
	}
}
