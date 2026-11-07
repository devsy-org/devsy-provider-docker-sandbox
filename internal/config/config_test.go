package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testCPUs       = "SBX_CPUS"
	testMemory     = "SBX_MEMORY"
	testPath       = "SBX_PATH"
	testDockerDisk = "SBX_DOCKER_DISK"
	testRootDisk   = "SBX_ROOT_DISK"
)

func TestLoadRejectsInvalidResources(t *testing.T) {
	cases := []struct{ key, value string }{
		{testCPUs, "0"},
		{testCPUs, "999999"},
		{testMemory, "511m"},
		{testMemory, "4G"},
		{testMemory, "9223372036854775807g"},
		{testRootDisk, "7g"},
		{testDockerDisk, "511m"},
		{testDockerDisk, "6g"},
		{testDockerDisk, "7167m"},
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
		testPath:       os.Args[0],
		testCPUs:       "invalid",
		testMemory:     "garbage",
		testRootDisk:   "bad",
		testDockerDisk: "bad",
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

func TestDockerDiskMinimum(t *testing.T) {
	below := func(value string) string {
		return "SBX_DOCKER_DISK: size \"" + value + "\" is below the minimum of 7g"
	}
	cases := []struct {
		value   string
		want    int64
		wantErr string
	}{
		{"512m", 0, below("512m")},
		{"1g", 0, below("1g")},
		{"4g", 0, below("4g")},
		{"6g", 0, below("6g")},
		{"7167m", 0, below("7167m")},
		{"7g", 7 << 30, ""},
		{"7168m", 7 << 30, ""},
		{"8g", 8 << 30, ""},
		{"40g", 40 << 30, ""},
	}
	for _, tt := range cases {
		t.Run(tt.value, func(t *testing.T) {
			opts := map[string]string{testPath: os.Args[0], testDockerDisk: tt.value}
			cfg, err := Load(func(k string) string { return opts[k] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) ||
					!strings.Contains(err.Error(), "E_CONFIG_DRIFT") {
					t.Fatalf("got %v, want E_CONFIG_DRIFT containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || cfg.Spec.DockerDiskBytes != tt.want {
				t.Fatalf("got %d, %v, want %d", cfg.Spec.DockerDiskBytes, err, tt.want)
			}
		})
	}
}

func TestRootDiskMinimumMessage(t *testing.T) {
	opts := map[string]string{testPath: os.Args[0], testRootDisk: "7g"}
	_, err := Load(func(k string) string { return opts[k] })
	if err == nil ||
		!strings.Contains(err.Error(), "SBX_ROOT_DISK: size \"7g\" is below the minimum of 8g") {
		t.Fatalf("unexpected error %v", err)
	}
}
