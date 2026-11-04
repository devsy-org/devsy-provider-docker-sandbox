//go:build native

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nativeHarness struct {
	devsy string
	home  string
	alias string
	root  string
	ctx   context.Context
}

func TestNativeWorkspace(t *testing.T) {
	devsy := requiredEnv(t, "DEVSY_NATIVE_CORE_BIN")
	directory := requiredEnv(t, "DEVSY_NATIVE_RELEASE_DIR")
	version := requiredEnv(t, "DEVSY_NATIVE_VERSION")
	sbx := requiredEnv(t, "SBX_PATH")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	for _, args := range [][]string{{"version", "--format", "json"}, {"ls", "--format", "json"}} {
		command := exec.CommandContext(ctx, sbx, args...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("authenticated native sbx %s failed: %v: %s", args[0], err, output)
		}
	}
	root, err := os.MkdirTemp("", "devsy-native-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("retained native workspace state at %s", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove native workspace state: %v", err)
		}
	})
	manifest, err := os.ReadFile(filepath.Join(directory, "provider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(directory)))
	defer server.Close()
	prefix := "https://github.com/devsy-org/devsy-provider-docker-sandbox/releases/download/" + version + "/"
	if !bytes.Contains(manifest, []byte(prefix)) {
		t.Fatal("candidate manifest does not name the candidate version")
	}
	manifest = bytes.ReplaceAll(manifest, []byte(prefix), []byte(server.URL+"/"))
	manifestPath := filepath.Join(root, "provider.yaml")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	h := &nativeHarness{devsy: devsy, home: filepath.Join(root, "devsy-home"), alias: "sandbox-native", root: root, ctx: ctx}
	h.run(t, nil, "provider", "add", "--use=false", "--name", h.alias, "-o", "SBX_PATH="+sbx, manifestPath)
	h.run(t, nil, "provider", "init", h.alias)
	image := h.createWorkspace(t, "image")
	t.Run("image hooks and source persistence", func(t *testing.T) {
		assertGuest(t, h, image, "test -f /tmp/devsy-sandbox-post-create")
		h.run(t, nil, "workspace", "exec", image, "--", "sh", "-c", "printf machine-data > /tmp/devsy-sandbox-persist")
		h.run(t, nil, "workspace", "stop", image)
		h.run(t, nil, "workspace", "up", "--ide", "none", image)
		assertGuest(t, h, image, "test \"$(cat /tmp/devsy-sandbox-persist)\" = machine-data")
	})
	t.Run("stream transport", func(t *testing.T) { exerciseNativeTransport(t, h, image) })
	t.Run("Dockerfile and Feature", func(t *testing.T) {
		id := h.createWorkspace(t, "dockerfile")
		assertGuest(t, h, id, "test \"$(cat /tmp/devsy-sandbox-dockerfile)\" = dockerfile-ready")
		assertGuest(t, h, id, "test \"$(cat /tmp/devsy-sandbox-feature)\" = feature-ready")
	})
	t.Run("Compose and volume", func(t *testing.T) {
		id := h.createWorkspace(t, "compose")
		assertGuest(t, h, id, "test \"$(cat /tmp/devsy-sandbox-compose)\" = compose-ready")
		assertGuest(t, h, id, "test -d /var/lib/sandbox-data")
		assertGuest(t, h, id, "timeout 20 bash -c 'until (echo > /dev/tcp/database/5432) >/dev/null 2>&1; do sleep 1; done'")
	})
	t.Run("bash zsh and fish shells", func(t *testing.T) {
		id := h.createWorkspace(t, "shell")
		assertGuest(t, h, id, "test -f /tmp/devsy-sandbox-shells")
		assertGuest(t, h, id, "bash -lc 'test -n \"$PATH\" && test -n \"$USER\"'")
		assertGuest(t, h, id, "zsh -lc 'test -n \"$PATH\" && test -n \"$USER\"'")
		assertGuest(t, h, id, "fish -lc 'test -n \"$PATH\"; and test -n \"$USER\"'")
	})
	t.Run("allowed build and denied runtime network", func(t *testing.T) {
		deniedURL := requiredEnv(t, "SBX_NATIVE_DENIED_URL")
		before := sandboxNames(t, h.ctx, sbx)
		id := h.createWorkspace(t, "network")
		created := newSandboxName(t, before, sandboxNames(t, h.ctx, sbx))
		assertGuest(t, h, id, "test -s /tmp/devsy-sandbox-allowed-https")
		assertGuest(t, h, id, "curl --fail --silent --show-error --max-time 30 https://registry.npmjs.org/ >/dev/null")
		command := exec.CommandContext(h.ctx, h.devsy, "--no-status", "workspace", "exec", id, "--", "sh", "-c", "if curl --fail --silent --max-time 10 --output /dev/null -- \"$1\" >/dev/null 2>&1; then exit 1; fi", "sh", deniedURL)
		command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("guest reached the denied destination: %v: %s", err, output)
		}
		if decision := policyDecision(t, h.ctx, sbx, created, deniedURL); decision.Allowed || decision.DenyKind == "" {
			t.Fatalf("sbx policy does not deny %s for the sandbox: %+v", deniedURL, decision)
		}
		if decision := policyDecision(t, h.ctx, sbx, created, "https://registry.npmjs.org/"); !decision.Allowed {
			t.Fatalf("sbx policy does not allow the build destination: %+v", decision)
		}
	})
}

type networkDecision struct {
	Allowed  bool   `json:"allowed"`
	DenyKind string `json:"deny_kind"`
}

func policyDecision(t *testing.T, ctx context.Context, sbx, sandbox, url string) networkDecision {
	t.Helper()
	output, err := exec.CommandContext(ctx, sbx, "policy", "check", "network", "--sandbox", sandbox, "--json", url).Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("sbx policy check for %s failed: %v", url, err)
	}
	var decision networkDecision
	if err := json.Unmarshal(output, &decision); err != nil {
		t.Fatalf("sbx policy check for %s returned unreadable output: %v: %s", url, err, output)
	}
	return decision
}

func sandboxNames(t *testing.T, ctx context.Context, sbx string) map[string]bool {
	t.Helper()
	output, err := exec.CommandContext(ctx, sbx, "ls", "--format", "json").Output()
	if err != nil {
		t.Fatalf("sbx inventory failed: %v", err)
	}
	var items []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(output, &items); err != nil {
		t.Fatalf("unreadable sbx inventory: %v", err)
	}
	names := make(map[string]bool, len(items))
	for _, item := range items {
		names[item.Name] = true
	}
	return names
}

func newSandboxName(t *testing.T, before, after map[string]bool) string {
	t.Helper()
	var created []string
	for name := range after {
		if !before[name] {
			created = append(created, name)
		}
	}
	if len(created) != 1 {
		t.Fatalf("workspace creation added %d sandboxes, want 1", len(created))
	}
	return created[0]
}

func (h *nativeHarness) createWorkspace(t *testing.T, fixture string) string {
	t.Helper()
	id := fmt.Sprintf("sandbox-%s-%d", fixture, time.Now().UnixNano())
	folder := filepath.Join(h.root, id)
	if err := copyFixture(filepath.Join("fixtures", fixture), folder); err != nil {
		t.Fatal(err)
	}
	created := false
	t.Cleanup(func() {
		if !created {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, h.devsy, "--no-status", "workspace", "delete", id)
		command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("scoped workspace cleanup failed; retain provider state under %s: %v: %s", h.home, err, output)
		}
	})
	created = true
	h.run(t, nil, "workspace", "up", "--ide", "none", "--id", id, "--provider", h.alias, folder)
	return id
}

func (h *nativeHarness) run(t *testing.T, stdin []byte, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(h.ctx, h.devsy, append([]string{"--no-status"}, args...)...)
	command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Devsy %v failed: %v: %s", args[:min(2, len(args))], err, output)
	}
	return output
}

func assertGuest(t *testing.T, h *nativeHarness, id, script string) {
	t.Helper()
	h.run(t, nil, "workspace", "exec", id, "--", "sh", "-c", script)
}

func copyFixture(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported fixture entry %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		return errors.Join(err, output.Close())
	})
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("native prerequisite %s is not configured", name)
	}
	return value
}
