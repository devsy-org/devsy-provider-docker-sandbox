package provider

import (
	"context"
	"fmt"
)

func (p Provider) Describe(ctx context.Context) (string, error) {
	r, e := p.load()
	if e != nil {
		return "", e
	}
	status, e := p.Status(ctx)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf(`Machine: %s
Sandbox: %s
State: %s
CPUs: %d
Memory: %d bytes
Root disk: %d bytes
Docker disk: %d bytes
Storage: private guest, mountless
`, r.MachineID, r.SandboxName, status, r.Spec.CPUs, r.Spec.MemoryBytes, r.Spec.RootDiskBytes, r.Spec.DockerDiskBytes), nil
}
