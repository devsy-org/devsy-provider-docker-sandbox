package sbx

import (
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"strconv"
)

func CreateArgs(name string, spec config.Spec) []string {
	return []string{"create", "--name", name, "--skills", "off", "--cpus", strconv.Itoa(spec.CPUs), "--memory", strconv.FormatInt(spec.MemoryBytes/(1<<20), 10) + "m", "--no-progress", "shell"}
}
func CreateEnv(spec config.Spec) []string {
	return []string{"DOCKER_SANDBOXES_ROOT_SIZE=" + strconv.FormatInt(spec.RootDiskBytes/(1<<20), 10) + "m", "DOCKER_SANDBOXES_DOCKER_SIZE=" + strconv.FormatInt(spec.DockerDiskBytes/(1<<20), 10) + "m"}
}
func RunArgs(name string) []string    { return []string{"run", "--detach", "--name", name} }
func StopArgs(name string) []string   { return []string{"stop", "--format", "json", name} }
func RemoveArgs(name string) []string { return []string{"rm", "--force", "--format", "json", name} }
