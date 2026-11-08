package probe

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
)

type Check struct {
	Program   string
	Expected  string
	disk      diskRequirement
	diskProbe bool
}

type diskRequirement struct {
	rootBytes   int64
	dockerBytes int64
}

const ready = "ready"

func Checks(spec config.Spec) []Check {
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}
	return []Check{
		{Program: "uname -s; uname -m", Expected: fmt.Sprintf("Linux%c%s%c", 10, arch, 10)},
		{
			Program:  "test -x /bin/sh && command -v tar >/dev/null && command -v docker >/dev/null && printf ready",
			Expected: ready,
		},
		{
			Program: `mkdir -p /var/lib/devsy/agent && d=$(mktemp -d /var/lib/devsy/agent/.probe.XXXXXXXX) && ` +
				`printf tested > "$d/test" && test "$(cat "$d/test")" = tested && rm -rf "$d" && printf ready`,
			Expected: ready,
		},
		{
			Program: `DOCKER_CONTEXT= DOCKER_TLS_VERIFY= DOCKER_CERT_PATH= docker --host unix:///var/run/docker.sock ` +
				`version --format '{{.Server.Os}}'`,
			Expected: "linux",
		},
		{
			Program:  "docker buildx version >/dev/null && docker compose version --short >/dev/null && printf ready",
			Expected: ready,
		},
		{
			Program: `test -S /var/run/docker.sock && root_device=$(stat -c %d /) && ` +
				`docker_device=$(stat -c %d /var/lib/docker) && root_capacity=$(df -B1 --output=size,avail / | tail -n 1) && ` +
				`docker_capacity=$(df -B1 --output=size,avail /var/lib/docker | tail -n 1) && ` +
				`printf '%s %s\n%s\n%s\n' "$root_device" "$docker_device" "$root_capacity" "$docker_capacity"`,
			disk:      diskRequirement{spec.RootDiskBytes, spec.DockerDiskBytes},
			diskProbe: true,
		},
	}
}

func Match(check Check, data []byte) error {
	if check.diskProbe {
		return matchDiskCapacity(check.disk, data)
	}
	if strings.TrimSpace(string(data)) != strings.TrimSpace(check.Expected) {
		return errors.New("guest capability check failed")
	}
	return nil
}

func matchDiskCapacity(requirement diskRequirement, data []byte) error {
	failed := errors.New("guest capability check failed")
	fields := strings.Fields(string(data))
	if requirement.rootBytes <= 0 || requirement.dockerBytes <= 0 || len(fields) != 6 {
		return failed
	}
	var values [6]int64
	for i, field := range fields {
		value, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			return failed
		}
		values[i] = value
	}
	if values[0] == values[1] {
		return failed
	}
	if err := checkCapacity(
		"root disk (SBX_ROOT_DISK)",
		values[2],
		values[3],
		requirement.rootBytes,
	); err != nil {
		return err
	}
	return checkCapacity(
		"Docker disk (SBX_DOCKER_DISK)",
		values[4],
		values[5],
		requirement.dockerBytes,
	)
}

func checkCapacity(name string, capacity, available, requested int64) error {
	if validCapacity(capacity, available, requested) {
		return nil
	}
	if capacity >= 0 && capacity < minimumCapacity(requested) {
		return fmt.Errorf(
			"guest capability check failed: %s requested %s, guest reports %s, %.2f percent short "+
				"of the request and past the 3 percent allowance; a larger size such as %dg may pass",
			name, describeBytes(requested), describeBytes(capacity),
			float64(requested-capacity)*100/float64(requested), suggestedGiB(requested),
		)
	}
	return errors.New("guest capability check failed")
}

func describeBytes(n int64) string {
	return fmt.Sprintf("%d bytes (%.2f GiB)", n, float64(n)/(1<<30))
}

func suggestedGiB(requested int64) int64 {
	next := (requested+(1<<30)-1)>>30 + 1
	return max(next, 8)
}

func minimumCapacity(requested int64) int64 {
	return requested - (requested/100)*3 - (requested%100)*3/100
}

func validCapacity(capacity, available, requested int64) bool {
	return capacity >= minimumCapacity(requested) && capacity <= requested && available > 0 &&
		available <= capacity
}
