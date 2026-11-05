//go:build !windows

package compat

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func hostVersion() error {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH != "arm64" {
			return Fail("E_HOST_UNSUPPORTED", "Docker Sandboxes requires Apple silicon on macOS")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion")
		cmd.Env = HostEnvironment()
		out, e := cmd.Output()
		if e != nil {
			return Wrap("E_HOST_UNSUPPORTED", e)
		}
		segments := strings.Split(strings.TrimSpace(string(out)), ".")
		major, e := strconv.Atoi(segments[0])
		if e != nil || major < 14 {
			return Fail("E_HOST_UNSUPPORTED", "macOS 14 or later is required")
		}
		return nil
	case "linux":
		if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
			return Fail("E_HOST_UNSUPPORTED", "only Ubuntu amd64 and arm64 are supported")
		}
		data, e := os.ReadFile("/etc/os-release")
		if e != nil {
			return Wrap("E_HOST_UNSUPPORTED", e)
		}
		var distribution, version string
		for line := range strings.SplitSeq(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			value = strings.Trim(value, "\"")
			if key == "ID" {
				distribution = value
			}
			if key == "VERSION_ID" {
				version = value
			}
		}
		parts := strings.Split(version, ".")
		if distribution != "ubuntu" || len(parts) < 2 {
			return Fail("E_HOST_UNSUPPORTED", "Ubuntu 24.04 or later is required")
		}
		major, e := strconv.Atoi(parts[0])
		if e != nil {
			return Wrap("E_HOST_UNSUPPORTED", e)
		}
		minor, e := strconv.Atoi(parts[1])
		if e != nil {
			return Wrap("E_HOST_UNSUPPORTED", e)
		}
		if major < 24 || major == 24 && minor < 4 {
			return Fail("E_HOST_UNSUPPORTED", "Ubuntu 24.04 or later is required")
		}
		return nil
	default:
		return Fail("E_HOST_UNSUPPORTED", "unsupported host operating system %s", runtime.GOOS)
	}
}
