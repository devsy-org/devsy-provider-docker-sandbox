//go:build ignore

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/cmd"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "internal" {
		cmd.Execute()
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 5 || (os.Args[1] != "run" && os.Args[1] != "cancel") {
		return errors.New("usage: native_cancel run|cancel WORKSPACE_ID GUEST_COMMAND DEVSY_BINARY")
	}
	cfg, err := config.LoadConfig("", "")
	if err != nil {
		return err
	}
	ws, err := provider.LoadWorkspaceConfig(cfg.DefaultContext, os.Args[2])
	if err != nil {
		return err
	}
	machine, err := provider.LoadMachineConfig(cfg.DefaultContext, ws.Machine.ID)
	if err != nil {
		return err
	}
	installed, err := workspace.FindProvider(cfg, machine.Provider.Name)
	if err != nil {
		return err
	}
	if installed.Config.Exec.Cancellation != provider.ProviderCancellationFile {
		return errors.New("installed provider has no cooperative cancellation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	operation := func() error {
		return clientimplementation.RunCommandWithBinaries(
			ctx,
			clientimplementation.WorkspaceCommandConfig{
				Command:              installed.Config.Exec.Command,
				WorkspaceContextName: machine.Context,
				Machine:              machine,
				Options:              cfg.ProviderOptions(installed.Config.Name),
				Config:               installed.Config,
				Stdin:                bytes.NewReader(nil),
				Stdout:               io.Discard,
				Stderr:               os.Stderr,
				RawStdout:            true,
				ExtraEnv: map[string]string{
					provider.CommandEnv:  os.Args[3],
					config.EnvBinaryPath: os.Args[4],
				},
			},
		)
	}
	if os.Args[1] == "run" {
		return operation()
	}
	completed := make(chan error, 1)
	go func() {
		completed <- operation()
	}()
	trigger := make(chan error, 1)
	go func() {
		var signal [1]byte
		_, readErr := os.Stdin.Read(signal[:])
		trigger <- readErr
	}()
	select {
	case err := <-completed:
		if err == nil {
			return errors.New("guest command ended before cancellation")
		}
		return fmt.Errorf("guest command ended before cancellation: %w", err)
	case err := <-trigger:
		if err != nil {
			return fmt.Errorf("read cancellation request: %w", err)
		}
	}
	cancel()
	if err := <-completed; err == nil {
		return errors.New("Devsy command unexpectedly completed after cancellation")
	} else if !errors.Is(err, context.Canceled) {
		return fmt.Errorf("Devsy command did not acknowledge cancellation: %w", err)
	}
	return nil
}
