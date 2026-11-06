//go:build native

package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/probe"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

type crashHarness struct {
	t       *testing.T
	ctx     context.Context
	binary  string
	backend string
	env     []string
	store   state.Store
}

func TestNativeProviderCrash(t *testing.T) {
	h := newCrashHarness(t)
	h.run("create")
	h.run("stop")
	attempt := h.crashStart()
	h.recoverStart(attempt)
	h.assertRunning()
	r := h.record()
	h.run("delete")
	if sandboxNames(t, h.ctx, h.backend)[r.SandboxName] {
		t.Fatal("delete left an orphan sandbox")
	}
	output, err := h.command("status").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("E_MACHINE_MISSING")) {
		t.Fatalf("deleted owned machine did not fail closed: %v: %s", err, output)
	}
	deleted := h.record()
	if deleted.Phase != "deleted" || deleted.Operation != nil || deleted.SandboxID != r.SandboxID {
		t.Fatal("delete did not retain the settled owned identity")
	}
	t.Log("delete verified absent in real sbx inventory; deleted journal retains owned ID")
}

func (h *crashHarness) crashStart() state.Attempt {
	h.t.Helper()
	sessions, err := h.store.Acquire(h.ctx, "sessions", true)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() {
		if err := sessions.Close(); err != nil {
			h.t.Error(err)
		}
	}()
	first := h.command("start")
	var diagnostics bytes.Buffer
	first.Stderr = &diagnostics
	if err := first.Start(); err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = first.Process.Kill(); _ = first.Wait() }()
	h.wait(func() bool {
		output, err := h.command("status").Output()
		return err == nil && strings.TrimSpace(string(output)) == "Busy"
	})
	h.assertConcurrentBlocked()
	if err := sessions.Close(); err != nil {
		h.t.Fatal(err)
	}
	sessions = nil
	attempt := h.waitStartedRun()
	if err := first.Process.Kill(); err != nil {
		h.t.Fatal(err)
	}
	if err := first.Wait(); err == nil {
		h.t.Fatal("provider completed before hard termination")
	} else {
		if runtime.GOOS != "windows" && err.Error() != "signal: killed" {
			h.t.Fatalf("provider was not killed by SIGKILL: %v: %s", err, diagnostics.String())
		}
		h.t.Logf("hard-killed provider during start: %v", err)
	}
	return attempt
}

func (h *crashHarness) waitStartedRun() state.Attempt {
	h.t.Helper()
	var attempt state.Attempt
	h.wait(func() bool {
		r, err := h.store.Load()
		if err != nil || r.Operation == nil || r.Operation.Kind != "start" {
			return false
		}
		a, err := h.store.ReadAttempt(r.Operation.LastAttemptID)
		if err != nil || a.Kind != "run" || a.Phase != "started" {
			return false
		}
		attempt = a
		return true
	})
	return attempt
}

func (h *crashHarness) recoverStart(attempt state.Attempt) {
	h.t.Helper()
	r := h.record()
	if r.Operation == nil || r.Operation.ID != attempt.OperationID {
		h.t.Fatal("interrupted start operation was lost")
	}
	output, err := h.command("status").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("E_UNCLEAN_ATTEMPT")) {
		h.t.Fatalf("status accepted interrupted child: %v: %s", err, output)
	}
	h.terminateAttempt(attempt)
	h.run(
		"recover",
		"--machine-key",
		r.MachineKey,
		"--attempt",
		attempt.AttemptID,
		"--acknowledge-client-terminated",
	)
	r = h.record()
	if r.Operation == nil || r.Operation.Phase != "uncertain" {
		h.t.Fatal("journal did not mark start uncertain")
	}
	h.t.Log("journal marks interrupted start uncertain")
	h.waitBackend(r)
	h.settleOperation(r)
}

func (h *crashHarness) settleOperation(r state.Record) {
	h.t.Helper()
	output, err := h.command(h.backendRecoveryArgs(r)...).CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("E_OPERATION_UNCERTAIN")) {
		h.t.Fatalf(
			"backend acknowledgement lost explicit reconciliation requirement: %v: %s",
			err,
			output,
		)
	}
	r = h.record()
	if r.Operation != nil || r.LastErrorCode != "E_OPERATION_UNCERTAIN" {
		h.t.Fatal("recover did not settle operation")
	}
}

func (h *crashHarness) backendRecoveryArgs(r state.Record) []string {
	return []string{
		"recover", "--machine-key", h.store.Key,
		"--operation", r.Operation.ID, "--acknowledge-backend-settled",
	}
}

func (h *crashHarness) assertRunning() {
	h.t.Helper()
	h.run("start")
	if output := h.run("status"); strings.TrimSpace(string(output)) != "Running" {
		h.t.Fatalf("reconciled status: %s", output)
	}
	r := h.record()
	check := probe.Checks(r.Spec)[0]
	output, err := exec.CommandContext(h.ctx, h.backend, sbx.ExecArgs(r.SandboxName, check.Program)...).
		Output()
	if err != nil {
		h.t.Fatal(err)
	}
	if err := probe.Match(check, output); err != nil {
		h.t.Fatal(err)
	}
	h.t.Log("recover settled operation; explicit start and status confirm Running")
}

func newCrashHarness(t *testing.T) *crashHarness {
	t.Helper()
	backend := requiredEnv(t, "SBX_PATH")
	core := requiredEnv(t, "DEVSY_NATIVE_CORE_BIN")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	t.Cleanup(cancel)
	root := t.TempDir()
	binary := filepath.Join(root, "provider")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-o",
		binary,
		"./cmd/devsy-provider-docker-sandbox",
	)
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v: %s", err, output)
	}
	folder := filepath.Join(root, "machine")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := config.Key(folder)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	h := &crashHarness{t: t, ctx: ctx, binary: binary, backend: backend, store: store}
	h.env = append(
		os.Environ(),
		"SBX_PATH="+backend,
		"DEVSY="+core,
		"MACHINE_FOLDER="+folder,
		"MACHINE_ID=native-crash",
		"MACHINE_CONTEXT=native-crash",
		"MACHINE_PROVIDER=native-crash",
		"PROVIDER_ID=native-crash",
		"PROVIDER_CONTEXT=native-crash",
		"PROVIDER_FOLDER="+root,
		"DEVSY_PROVIDER_CANCEL_FILE=",
	)
	t.Cleanup(h.cleanup)
	return h
}

func (h *crashHarness) command(args ...string) *exec.Cmd {
	command := exec.CommandContext(h.ctx, h.binary, args...)
	command.Env = h.env
	return command
}

func (h *crashHarness) run(args ...string) []byte {
	h.t.Helper()
	output, err := h.command(args...).CombinedOutput()
	if err != nil {
		h.t.Fatalf("provider %s: %v: %s", args[0], err, output)
	}
	return output
}

func (h *crashHarness) record() state.Record {
	h.t.Helper()
	r, err := h.store.Load()
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *crashHarness) wait(condition func() bool) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 90*time.Second)
	defer cancel()
	for !condition() {
		select {
		case <-ctx.Done():
			h.t.Fatal("native crash precondition timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (h *crashHarness) assertConcurrentBlocked() {
	h.t.Helper()
	directory := filepath.Join(h.t.TempDir(), "invocation")
	if err := os.Mkdir(directory, 0o700); err != nil {
		h.t.Fatal(err)
	}
	marker := filepath.Join(directory, "cancel")
	attempts, err := h.store.Attempts()
	if err != nil || len(attempts) == 0 {
		h.t.Fatalf("read completed attempts: %v", err)
	}
	second := h.command("recover", "--machine-key", h.store.Key, "--attempt",
		attempts[0].AttemptID, "--acknowledge-client-terminated")
	second.Env = append(second.Env, "DEVSY_PROVIDER_CANCEL_FILE="+marker)
	var output bytes.Buffer
	second.Stderr = &output
	if err := second.Start(); err != nil {
		h.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- second.Wait() }()
	select {
	case err := <-done:
		h.t.Fatalf("concurrent mutation escaped lock: %v: %s", err, output.String())
	case <-time.After(500 * time.Millisecond):
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		h.t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 ||
			!strings.Contains(output.String(), "E_STORAGE: context canceled") {
			h.t.Fatalf("blocked mutation cancellation: %v: %s", err, output.String())
		}
	case <-time.After(10 * time.Second):
		_ = second.Process.Kill()
		<-done
		h.t.Fatal("concurrent mutation did not acknowledge host cancellation")
	}
	h.t.Log(
		"second provider mutation blocked by first provider lock and cancelled at mutation lock",
	)
}

func (h *crashHarness) terminateAttempt(a state.Attempt) {
	h.t.Helper()
	if a.DiagnosticPID == nil {
		h.t.Fatal("started attempt has no child PID")
	}
	child, err := os.FindProcess(*a.DiagnosticPID)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := child.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		h.t.Logf("orphan client already exited: %v", err)
	}
	if err := child.Release(); err != nil {
		h.t.Error(err)
	}
}

func (h *crashHarness) waitBackend(r state.Record) {
	h.t.Helper()
	profile, err := compat.Load()
	if err != nil {
		h.t.Fatal(err)
	}
	client := sbx.Client{Path: h.backend, Store: h.store}
	h.wait(func() bool {
		items, err := client.Inventory(h.ctx, profile)
		if err != nil {
			return false
		}
		item, found := items.Find(r.SandboxName)
		return found && item.ID == r.SandboxID &&
			(item.State == "running" || item.State == "stopped")
	})
}

func (h *crashHarness) cleanup() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	h.ctx = ctx
	attempts, err := h.store.Incomplete()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Error(err)
	}
	for _, a := range attempts {
		if a.DiagnosticPID != nil {
			h.terminateAttempt(a)
		}
		if output, err := h.command(
			"recover",
			"--machine-key",
			h.store.Key,
			"--attempt",
			a.AttemptID,
			"--acknowledge-client-terminated",
		).CombinedOutput(); err != nil {
			h.t.Errorf("cleanup recover: %v: %s", err, output)
		}
	}
	r, err := h.store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		h.t.Error(err)
		return
	}
	if r.Operation != nil && r.Operation.Phase == "uncertain" {
		_, _ = h.command(h.backendRecoveryArgs(r)...).
			CombinedOutput()
	}
	output, err := h.command("delete", "--machine-key", h.store.Key).CombinedOutput()
	if err != nil {
		h.t.Errorf("cleanup delete: %v: %s", err, output)
		return
	}
	if sandboxNames(h.t, ctx, h.backend)[r.SandboxName] {
		h.t.Error("cleanup left sandbox behind")
	}
}
