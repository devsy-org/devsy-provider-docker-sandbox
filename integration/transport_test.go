//go:build native

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func exerciseNativeTransport(t *testing.T, h *nativeHarness, id string) {
	t.Helper()
	t.Run("binary echo and input EOF", func(t *testing.T) {
		input := []byte{0, 255, 128, '\r', '\n', 'x', 0}
		output := guestCommand(t, h, id, input, "cat")
		if !bytes.Equal(input, output) {
			t.Fatalf("guest byte stream differs: input %x, output %x", input, output)
		}
	})
	t.Run("simultaneous backpressure", func(t *testing.T) {
		input := bytes.Repeat([]byte{0, 128, 255, '\r', '\n', 'x'}, 2*1024*1024)
		output := guestCommand(t, h, id, input, "cat")
		if !bytes.Equal(input, output) {
			t.Fatal("12 MiB guest stream differs from input")
		}
	})
	t.Run("independent stderr", func(t *testing.T) {
		command := exec.CommandContext(
			h.ctx,
			h.devsy,
			"--no-status",
			"--result-format",
			"plain",
			"workspace",
			"exec",
			id,
			"--",
			"sh",
			"-c",
			"printf out; printf err >&2",
		)
		command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != "out" || stderr.String() != "err" {
			t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
		}
	})
	t.Run("guest exit codes", func(t *testing.T) {
		for _, code := range []int{0, 7, 125, 127} {
			command := exec.CommandContext(
				h.ctx,
				h.devsy,
				"--no-status",
				"workspace",
				"exec",
				id,
				"--",
				"sh",
				"-c",
				fmt.Sprintf("exit %d", code),
			)
			command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
			err := command.Run()
			if code == 0 {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			status, ok := err.(*exec.ExitError)
			if !ok || status.ExitCode() != code {
				t.Fatalf("guest exit %d became %v", code, err)
			}
		}
	})
	t.Run("concurrent sessions", func(t *testing.T) {
		var group sync.WaitGroup
		errors := make(chan error, 2)
		for index := range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				command := exec.CommandContext(
					h.ctx,
					h.devsy,
					"--no-status",
					"workspace",
					"exec",
					id,
					"--",
					"sh",
					"-c",
					fmt.Sprintf("sleep 2; printf '%d'", index),
				)
				command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
				output, err := command.Output()
				if err == nil && string(output) != fmt.Sprintf("%d", index) {
					err = fmt.Errorf("session %d returned %q", index, output)
				}
				errors <- err
			}()
		}
		group.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("cancel admitted guest process without stopping another session", func(t *testing.T) {
		exerciseNativeCancellation(t, h, id)
	})
}

func guestCommand(
	t *testing.T,
	h *nativeHarness,
	id string,
	input []byte,
	arguments ...string,
) []byte {
	t.Helper()
	args := append([]string{"--no-status", "workspace", "exec", id, "--"}, arguments...)
	command := exec.CommandContext(h.ctx, h.devsy, args...)
	command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("guest execution failed: %v: %s", err, stderr.Bytes())
	}
	return stdout.Bytes()
}

func exerciseNativeCancellation(t *testing.T, h *nativeHarness, id string) {
	t.Helper()
	helper := requiredEnv(t, "DEVSY_NATIVE_CANCEL_HELPER")
	target := fmt.Sprintf("/tmp/devsy-sandbox-cancel-target-%d", time.Now().UnixNano())
	unrelated := fmt.Sprintf("/tmp/devsy-sandbox-cancel-other-%d", time.Now().UnixNano())
	otherScript := fmt.Sprintf("sh -c 'printf \"%%s\\n\" \"$$\" > %s; exec sleep 300'", unrelated)
	other := exec.CommandContext(h.ctx, helper, "run", id, otherScript, h.devsy)
	other.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
	other.Stdout = io.Discard
	var otherStderr bytes.Buffer
	other.Stderr = &otherStderr
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanup := fmt.Sprintf(
			"sh -c 'if test -s %s; then read pid < %s; kill \"$pid\" 2>/dev/null || true; fi; rm -f %s %s'",
			unrelated,
			unrelated,
			unrelated,
			target,
		)
		if err := nativeGuestRun(ctx, h, helper, id, cleanup); err != nil {
			t.Errorf("scoped guest session cleanup failed: %v", err)
		}
		completed := make(chan error, 1)
		go func() { completed <- other.Wait() }()
		select {
		case <-completed:
		case <-time.After(10 * time.Second):
			if err := other.Process.Kill(); err != nil {
				t.Errorf("terminate orphaned test client: %v", err)
			}
			<-completed
			t.Error("unrelated test session did not exit after scoped guest cleanup")
		}
		if t.Failed() {
			t.Logf("unrelated session stderr: %s", otherStderr.String())
		}
	})
	waitForNativeGuest(
		t,
		h,
		helper,
		id,
		fmt.Sprintf(
			"sh -c 'test -s %s && read pid < %s && kill -0 \"$pid\"'",
			unrelated,
			unrelated,
		),
		3*time.Minute,
	)

	targetScript := fmt.Sprintf(
		"sh -c 'sleep 300 & child=$!; printf \"%%s %%s\\n\" \"$$\" \"$child\" > %s; wait \"$child\"'",
		target,
	)
	command := exec.CommandContext(h.ctx, helper, "cancel", id, targetScript, h.devsy)
	command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
	command.Stdout = io.Discard
	var commandStderr bytes.Buffer
	command.Stderr = &commandStderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	requested, waited := false, false
	t.Cleanup(func() {
		if waited {
			return
		}
		if !requested {
			if _, err := stdin.Write([]byte{'\n'}); err != nil {
				t.Errorf("request cleanup of target command: %v", err)
			}
		}
		if err := stdin.Close(); err != nil {
			t.Errorf("close target cancellation channel: %v", err)
		}
		if err := command.Wait(); err != nil {
			t.Errorf("target cancellation cleanup failed: %v: %s", err, commandStderr.String())
		}
	})
	waitForNativeGuest(
		t,
		h,
		helper,
		id,
		fmt.Sprintf(
			"sh -c 'test -s %s && read parent child < %s && kill -0 \"$parent\" && kill -0 \"$child\"'",
			target,
			target,
		),
		3*time.Minute,
	)
	if _, err := stdin.Write([]byte{'\n'}); err != nil {
		t.Fatal(err)
	}
	requested = true
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	waited = true
	if err != nil {
		t.Fatalf(
			"Devsy did not finish cooperative cancellation: %v: %s",
			err,
			commandStderr.String(),
		)
	}
	exited := fmt.Sprintf(
		"sh -c 'read parent child < %s; for pid in \"$parent\" \"$child\"; do state=$(ps -o stat= -p \"$pid\" 2>/dev/null || true); case \"$state\" in \"\"|*Z*) ;; *) exit 1;; esac; done'",
		target,
	)
	waitForNativeGuest(t, h, helper, id, exited, 2*time.Minute)
	if err := nativeGuestRun(
		h.ctx,
		h,
		helper,
		id,
		fmt.Sprintf("sh -c 'read pid < %s && kill -0 \"$pid\"'", unrelated),
	); err != nil {
		t.Fatalf(
			"unrelated admitted guest session did not survive: %v: %s",
			err,
			otherStderr.String(),
		)
	}
	if got := guestCommand(t, h, id, nil, "printf", "survived"); string(got) != "survived" {
		t.Fatalf("VM stopped after cancellation: %q", got)
	}
}

func nativeGuestRun(ctx context.Context, h *nativeHarness, helper, id, script string) error {
	command := exec.CommandContext(ctx, helper, "run", id, script, h.devsy)
	command.Env = append(os.Environ(), "DEVSY_HOME="+h.home)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func waitForNativeGuest(
	t *testing.T,
	h *nativeHarness,
	helper, id, script string,
	timeout time.Duration,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()
	for {
		probe, stopProbe := context.WithTimeout(ctx, time.Minute)
		err := nativeGuestRun(probe, h, helper, id, script)
		stopProbe()
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("guest process condition not observed before deadline: %v", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
