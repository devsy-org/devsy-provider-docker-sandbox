package sbx

import (
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"reflect"
	"testing"
)

func TestExactArgv(t *testing.T) {
	spec := config.Spec{CPUs: 2, MemoryBytes: 4 << 30, RootDiskBytes: 40 << 30, DockerDiskBytes: 48 << 30}
	cases := []struct{ got, want []string }{{CreateArgs("literal", spec), []string{"create", "--name", "literal", "--skills", "off", "--cpus", "2", "--memory", "4096m", "--no-progress", "shell"}}, {RunArgs("literal"), []string{"run", "--detach", "--name", "literal"}}, {StopArgs("literal"), []string{"stop", "--format", "json", "literal"}}, {RemoveArgs("literal"), []string{"rm", "--force", "--format", "json", "literal"}}, {ExecArgs("literal", "echo ' quoted $HOME"), []string{"exec", "--interactive", "--user", "root", "--workdir", "/", "literal", "--", "/bin/sh", "-c", "echo ' quoted $HOME"}}, {CreateEnv(spec), []string{"DOCKER_SANDBOXES_ROOT_SIZE=40960m", "DOCKER_SANDBOXES_DOCKER_SIZE=49152m"}}}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Fatalf("argv mismatch got %q want %q", c.got, c.want)
		}
	}
}
func TestStrictStructuredOutput(t *testing.T) {
	var result any
	for _, data := range []string{`{"x":1} {"x":2}`, "prefix {}", "["} {
		if e := exact([]byte(data), &result); e == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}
func TestChildEnvironmentPrivacy(t *testing.T) {
	t.Setenv("COMMAND", "user payload secret")
	t.Setenv("DEVSY_PROVIDER_CANCEL_FILE", "/tmp/marker")
	for _, entry := range childEnv(nil) {
		if entry == "COMMAND=user payload secret" || entry == "DEVSY_PROVIDER_CANCEL_FILE=/tmp/marker" {
			t.Fatal("reserved environment escaped into sbx child")
		}
	}
}
