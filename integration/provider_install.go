//go:build ignore

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/provider"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf(
			"usage: provider_install <devsy-executable> <artifact-directory> <release-tag>",
		)
	}
	devsy, directory, tag := os.Args[1], os.Args[2], os.Args[3]
	contents, err := os.ReadFile(filepath.Join(directory, "provider.yaml"))
	if err != nil {
		return err
	}
	server := httptest.NewServer(http.FileServer(http.Dir(directory)))
	defer server.Close()
	prefix := "https://github.com/devsy-org/devsy-provider-docker-sandbox/releases/download/" + tag + "/"
	if !bytes.Contains(contents, []byte(prefix)) {
		return fmt.Errorf("manifest does not refer to the release %s", tag)
	}
	contents = bytes.ReplaceAll(contents, []byte(prefix), []byte(server.URL+"/"))
	root, err := os.MkdirTemp("", "devsy-sandbox-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	manifestPath := filepath.Join(root, "provider.yaml")
	if err := os.WriteFile(manifestPath, contents, 0o600); err != nil {
		return err
	}
	cfg, err := provider.ParseProvider(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("real Devsy manifest parser: %w", err)
	}
	if cfg.Name != "docker-sandbox" || cfg.Version != tag || cfg.Agent.Driver != "docker" ||
		string(cfg.Agent.Local) != "false" ||
		!cfg.IsMachineProvider() {
		return fmt.Errorf("unexpected parsed provider configuration")
	}
	if string(cfg.Agent.Docker.Install) != "false" ||
		string(cfg.Agent.Docker.ConfigureDaemon) != "false" ||
		cfg.Agent.Docker.Env["DOCKER_HOST"] != "unix:///var/run/docker.sock" ||
		cfg.Exec.Cancellation != provider.ProviderCancellationFile {
		return fmt.Errorf(
			"manifest lacks guest Docker ownership or cooperative cancellation policy",
		)
	}
	if len(cfg.Binaries) != 1 || len(cfg.Binaries["DOCKER_SANDBOX_PROVIDER"]) == 0 {
		return fmt.Errorf("missing top-level executable declaration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	home := filepath.Join(root, "home")
	if err := install(ctx, devsy, home, manifestPath, "sandbox-alias", true); err != nil {
		return err
	}
	cfg.Name = "sandbox-alias"
	if err := os.Setenv("DEVSY_HOME", home); err != nil {
		return err
	}
	installed, err := provider.GetBinaries("default", cfg)
	if err != nil {
		return fmt.Errorf("resolve installed alias executable: %w", err)
	}
	binary := installed["DOCKER_SANDBOX_PROVIDER"]
	output, err := exec.CommandContext(ctx, binary, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run native installed executable: %w: %s", err, output)
	}
	if !strings.Contains(string(output), tag) {
		return fmt.Errorf("unexpected native installed executable version %q", output)
	}
	var originalChecksum string
	for _, entry := range cfg.Binaries["DOCKER_SANDBOX_PROVIDER"] {
		if entry.OS == runtime.GOOS && entry.Arch == runtime.GOARCH {
			originalChecksum = entry.Checksum
			break
		}
	}
	if originalChecksum == "" {
		return fmt.Errorf("manifest has no native executable")
	}
	corrupt := bytes.Replace(
		contents,
		[]byte("checksum: "+originalChecksum),
		[]byte("checksum: "+strings.Repeat("0", 64)),
		1,
	)
	if bytes.Equal(corrupt, contents) {
		return fmt.Errorf("cannot construct negative checksum case")
	}
	badManifest := filepath.Join(root, "corrupt.yaml")
	if err := os.WriteFile(badManifest, corrupt, 0o600); err != nil {
		return err
	}
	if err := install(
		ctx,
		devsy,
		filepath.Join(root, "bad-home"),
		badManifest,
		"sandbox-corrupt",
		false,
	); err != nil {
		return err
	}
	return nil
}

func install(ctx context.Context, devsy, home, manifest, alias string, shouldSucceed bool) error {
	command := exec.CommandContext(
		ctx,
		devsy,
		"provider",
		"add",
		"--use=false",
		"--name",
		alias,
		manifest,
	)
	command.Env = append(os.Environ(), "DEVSY_HOME="+home)
	output, err := command.CombinedOutput()
	if shouldSucceed && err != nil {
		return fmt.Errorf("alias provider installation failed: %w: %s", err, output)
	}
	if !shouldSucceed && err == nil {
		return fmt.Errorf("corrupt release checksum accepted by Devsy")
	}
	if !shouldSucceed && !bytes.Contains(bytes.ToLower(output), []byte("checksum")) {
		return fmt.Errorf("negative installation failed without a checksum error: %s", output)
	}
	return nil
}
