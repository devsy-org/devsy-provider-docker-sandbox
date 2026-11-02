package probe

import (
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"runtime"
	"strings"
)

type Check struct {
	Program  string
	Expected string
}

func Checks(spec config.Spec) []Check {
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}
	return []Check{
		{"uname -s; uname -m", fmt.Sprintf("Linux%c%s%c", 10, arch, 10)},
		{"test -x /bin/sh && command -v tar >/dev/null && command -v docker >/dev/null && printf ready", "ready"},
		{`mkdir -p /var/lib/devsy/agent && d=$(mktemp -d /var/lib/devsy/agent/.probe.XXXXXXXX) && printf tested > "$d/test" && test "$(cat "$d/test")" = tested && rm -rf "$d" && printf ready`, "ready"},
		{`DOCKER_CONTEXT= DOCKER_TLS_VERIFY= DOCKER_CERT_PATH= docker --host unix:///var/run/docker.sock version --format '{{.Server.Os}}'`, "linux"},
		{"docker buildx version >/dev/null && docker compose version --short >/dev/null && printf ready", "ready"},
		{fmt.Sprintf(`test -S /var/run/docker.sock && test "$(df -B1 --output=size / | tail -n 1)" -ge %d && test "$(df -B1 --output=size /var/lib/docker | tail -n 1)" -ge %d && test "$(df -B1 --output=avail / | tail -n 1)" -gt 0 && test "$(df -B1 --output=avail /var/lib/docker | tail -n 1)" -gt 0 && printf ready`, spec.RootDiskBytes, spec.DockerDiskBytes), "ready"},
	}
}
func Match(check Check, data []byte) error {
	if strings.TrimSpace(string(data)) != strings.TrimSpace(check.Expected) {
		return fmt.Errorf("guest capability check failed")
	}
	return nil
}
