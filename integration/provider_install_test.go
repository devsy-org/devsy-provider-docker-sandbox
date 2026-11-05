//go:build native

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeProviderInstallation(t *testing.T) {
	coreSource := requiredEnv(t, "DEVSY_NATIVE_CORE_SOURCE")
	devsy := requiredEnv(t, "DEVSY_NATIVE_CORE_BIN")
	directory := requiredEnv(t, "DEVSY_NATIVE_RELEASE_DIR")
	version := requiredEnv(t, "DEVSY_NATIVE_VERSION")
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"go",
		"run",
		filepath.Join(root, "provider_install.go"),
		devsy,
		directory,
		version,
	)
	command.Dir = coreSource
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native alias and checksum installation failed: %v: %s", err, output)
	}
}
