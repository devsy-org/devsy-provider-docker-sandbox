package qualify

import (
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

const Selection = "^TestNative"

const (
	LocalKind      = "local"
	TestOutputKind = "local-test-output"
)

var Gates = []string{
	"client_and_daemon",
	"inventory",
	"ownership",
	"mountless",
	"detached_lifetime",
	"byte_stream",
	"cancellation",
	"filesystem",
	"docker_capabilities",
	"daemon_ownership",
	"network",
	"devsy_workflow",
	"settings_authority",
	"sizing",
	"concurrency_crash",
	"native_install",
}

var RequiredTests = []string{
	"TestNativeWorkspace",
	"TestNativeProviderCrash",
	"TestNativeProviderInstallation",
}

type Evidence struct {
	CandidateCommit string `json:"candidate_commit"`
	CoreCommit      string `json:"core_commit"`
	ProviderVersion string `json:"provider_version"`
	Hosts           []Host `json:"hosts"`
}

type Host struct {
	Host                    string            `json:"host"`
	ClientVersion           string            `json:"client_version"`
	ClientRevision          string            `json:"client_revision"`
	DaemonVersion           string            `json:"daemon_version"`
	DaemonSchema            string            `json:"daemon_schema"`
	OperationResultSchema   string            `json:"operation_result_schema"`
	ShellSettingSchema      string            `json:"shell_setting_schema"`
	DiskSizingSchema        string            `json:"disk_sizing_schema"`
	DockerServerVersion     string            `json:"docker_server_version"`
	BuildxVersion           string            `json:"buildx_version"`
	ComposeVersion          string            `json:"compose_version"`
	HostOSVersion           string            `json:"host_os_version"`
	ProviderCommit          string            `json:"provider_commit"`
	FixtureProvenanceSHA256 string            `json:"fixture_provenance_sha256"`
	Gates                   map[string]string `json:"gates"`
	LocalRun                LocalRun          `json:"local_run"`
}

type LocalRun struct {
	Kind       string   `json:"kind"`
	LogSHA256  string   `json:"log_sha256"`
	StartedAt  string   `json:"started_at"`
	EndedAt    string   `json:"ended_at"`
	HostSHA256 string   `json:"host_sha256"`
	Selection  string   `json:"selection"`
	Tests      []string `json:"tests"`
}

type Run struct {
	Candidate        string
	CoreCommit       string
	Version          string
	Selection        string
	Partial          bool
	Host             string
	HostOSVersion    string
	Hostname         string
	ProvenanceSHA256 string
	SBXBefore        []byte
	SBXAfter         []byte
	Log              []byte
	ExitCode         int
	Started          time.Time
	Ended            time.Time
	Head             string
	Dirty            bool
}

type Expect struct {
	Candidate  string
	CoreCommit string
	Version    string
}

type (
	Profile = compat.Profile
	Tuple   = compat.Tuple
)
