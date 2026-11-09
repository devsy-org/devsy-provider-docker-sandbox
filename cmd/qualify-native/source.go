package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func output(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr.Bytes())
	}
	return out, nil
}

func gitState() (head string, dirty bool, err error) {
	out, err := output("git", "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	status, err := output("git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), len(bytes.TrimSpace(status)) > 0, nil
}

func sbxVersionJSON(sbx string) ([]byte, error) {
	if sbx == "" {
		return nil, errors.New("--sbx is required")
	}
	return output(sbx, "version", "--format", "json")
}

func hostOSVersion() (string, error) {
	if runtime.GOOS == "windows" {
		out, err := output("cmd", "/c", "ver")
		return "windows " + strings.TrimSpace(string(out)), err
	}
	out, err := output("uname", "-r")
	return runtime.GOOS + " " + strings.TrimSpace(string(out)), err
}

func currentHost() string { return runtime.GOOS + "/" + runtime.GOARCH }

func hostname() (string, error) { return os.Hostname() }

func readFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}

func writeFile(path string, data []byte) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.WriteFile(filepath.Base(path), data, 0o600)
}
