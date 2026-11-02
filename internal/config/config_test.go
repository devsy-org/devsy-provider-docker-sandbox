package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsInvalidResources(t *testing.T) {
	cases := []struct{ key, value string }{{"SBX_CPUS", "0"}, {"SBX_CPUS", "999999"}, {"SBX_MEMORY", "511m"}, {"SBX_MEMORY", "4G"}, {"SBX_MEMORY", "9223372036854775807g"}, {"SBX_ROOT_DISK", "7g"}, {"SBX_DOCKER_DISK", "511m"}, {"SBX_PATH", "relative/sbx"}, {"SBX_PATH", "sbx --arg"}}
	for _, tt := range cases {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			opts := map[string]string{"SBX_PATH": os.Args[0], tt.key: tt.value}
			_, err := Load(func(k string) string { return opts[k] })
			if err == nil {
				t.Fatal("invalid option accepted")
			}
		})
	}
}
func TestLoadAndCanonicalAlias(t *testing.T) {
	opts := map[string]string{"SBX_PATH": os.Args[0], "SBX_CPUS": "1", "MACHINE_ID": "m", "MACHINE_CONTEXT": "ctx", "MACHINE_PROVIDER": "first", "PROVIDER_ID": "p", "PROVIDER_CONTEXT": "ctx", "PROVIDER_FOLDER": t.TempDir()}
	folder := filepath.Join(t.TempDir(), "machine")
	if err := os.Mkdir(folder, 0700); err != nil {
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
	if first.Key != second.Key || first.Folder != second.Folder {
		t.Fatal("alias changed machine identity")
	}
	if len(first.Key) != 64 || strings.Contains(first.Key, "first") {
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
	opts := map[string]string{"SBX_PATH": os.Args[0], "SBX_CPUS": "invalid", "SBX_MEMORY": "garbage", "SBX_ROOT_DISK": "bad", "SBX_DOCKER_DISK": "bad"}
	c, e := LoadScoped(func(key string) string { return opts[key] })
	if e != nil {
		t.Fatal(e)
	}
	if c.Spec != (Spec{}) || c.SBXPath == "" {
		t.Fatal("scoped recovery inherited machine options")
	}
}
