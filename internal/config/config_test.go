package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testCPUs   = "SBX_CPUS"
	testMemory = "SBX_MEMORY"
	testPath   = "SBX_PATH"
)

func TestLoadRejectsInvalidResources(t *testing.T) {
	cases := []struct{ key, value string }{
		{testCPUs, "0"},
		{testCPUs, "999999"},
		{testMemory, "511m"},
		{testMemory, "4G"},
		{testMemory, "9223372036854775807g"},
		{"SBX_ROOT_DISK", "7g"},
		{"SBX_DOCKER_DISK", "511m"},
		{testPath, "relative/sbx"},
		{testPath, "sbx --arg"},
	}
	for _, tt := range cases {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			opts := map[string]string{testPath: os.Args[0], tt.key: tt.value}
			_, err := Load(func(k string) string { return opts[k] })
			if err == nil {
				t.Fatal("invalid option accepted")
			}
		})
	}
}

func TestLoadAndCanonicalAlias(t *testing.T) {
	opts := map[string]string{
		testPath:           os.Args[0],
		testCPUs:           "1",
		"MACHINE_ID":       "m",
		"MACHINE_CONTEXT":  "ctx",
		"MACHINE_PROVIDER": "first",
		"PROVIDER_ID":      "p",
		"PROVIDER_CONTEXT": "ctx",
		"PROVIDER_FOLDER":  t.TempDir(),
	}
	folder := filepath.Join(t.TempDir(), "machine")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	opts["MACHINE_FOLDER"] = folder
	load := func() (MachineIdentity, error) {
		cfg, e := Load(func(k string) string { return opts[k] })
		if e != nil {
			return MachineIdentity{}, e
		}
		return cfg.RequireMachine()
	}
	first, e := load()
	if e != nil {
		t.Fatal(e)
	}
	opts["MACHINE_PROVIDER"] = "renamed"
	second, e := load()
	if e != nil {
		t.Fatal(e)
	}
	if first.Key != second.Key {
		t.Fatal("alias changed machine identity")
	}
	if first.Folder != second.Folder {
		t.Fatal("alias changed machine identity")
	}
	if invalidTestKey(first.Key) {
		t.Fatalf("invalid key %s", first.Key)
	}
}

func TestRejectMissingIdentity(t *testing.T) {
	c := Config{Getenv: func(string) string { return "" }}
	if _, e := c.RequireMachine(); e == nil {
		t.Fatal("missing identity accepted")
	}
}

func TestScopedCleanupIgnoresMachineOptions(t *testing.T) {
	opts := map[string]string{
		testPath:          os.Args[0],
		testCPUs:          "invalid",
		testMemory:        "garbage",
		"SBX_ROOT_DISK":   "bad",
		"SBX_DOCKER_DISK": "bad",
	}
	c, e := LoadScoped(func(key string) string { return opts[key] })
	if e != nil {
		t.Fatal(e)
	}
	if c.Spec != (Spec{}) || c.SBXPath == "" {
		t.Fatal("scoped recovery inherited machine options")
	}
}

func invalidTestKey(key string) bool { return len(key) != 64 || strings.Contains(key, "first") }
