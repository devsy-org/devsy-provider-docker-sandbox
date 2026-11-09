package qualify

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

const (
	versionKey = "version"
	unknown    = "v9.9.9"
	candidate  = "5d19a767b88c2aa839680fccda972bac82a36da5"
	core       = "89d0d7eb7b0ff31c4847363ac45d87616a2cdb78"
	version    = "v0.1.2"
)

func read(t *testing.T, path string) []byte {
	t.Helper()
	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(strings.TrimPrefix(path, "../"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadProfile(t *testing.T) Profile {
	t.Helper()
	profile, err := compat.Load()
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

const fullLog = `=== RUN   TestNativeProviderCrash
--- PASS: TestNativeProviderCrash (60.00s)
=== RUN   TestNativeProviderInstallation
--- PASS: TestNativeProviderInstallation (8.00s)
=== RUN   TestNativeWorkspace
    workspace_test.go:150: native-evidence docker_server_version=29.1.3 buildx_version=v0.30.1 compose_version=2.40.3
--- PASS: TestNativeWorkspace (900.00s)
PASS
ok  	github.com/devsy-org/devsy-provider-docker-sandbox/integration	970.000s
`

func versionJSON(t *testing.T, profile Profile, revision string) []byte {
	t.Helper()
	client := profile.QualifiedSBXVersions[0]
	data, err := json.Marshal(map[string]any{
		"client": map[string]string{versionKey: client, "revision": revision},
		"server": map[string]string{
			"state":   "running",
			"version": profile.QualifiedDaemonVersions[client][0],
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func qualifiedVersionJSON(t *testing.T) []byte {
	t.Helper()
	profile := loadProfile(t)
	return versionJSON(t, profile, profile.QualifiedSBXRevisions[profile.QualifiedSBXVersions[0]])
}

func validRun(t *testing.T) Run {
	t.Helper()
	versionJSON := qualifiedVersionJSON(t)
	start := time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)
	return Run{
		Candidate: candidate, CoreCommit: core, Version: version, Selection: Selection,
		Host: "darwin/arm64", HostOSVersion: "darwin 25.6.0", Hostname: "maintainer-mac",
		ProvenanceSHA256: SHA256Hex([]byte("provenance")),
		SBXBefore:        versionJSON, SBXAfter: versionJSON,
		Log:     []byte(fullLog),
		Started: start, Ended: start.Add(40 * time.Minute),
		Head: candidate,
	}
}

func validEvidence(t *testing.T) Evidence {
	t.Helper()
	run := validRun(t)
	host, err := Build(loadProfile(t), run)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := Merge(nil, run, host)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestBuildRefusals(t *testing.T) {
	profile := loadProfile(t)
	staleRevision := versionJSON(t, profile, "0000000000000000000000000000000000000000")
	unavailable := read(t, "../sbx/testdata/v0.50.1/version-daemon-unavailable.json")
	cases := map[string]func(*Run){
		"dirty working tree":        func(r *Run) { r.Dirty = true },
		"HEAD is not the candidate": func(r *Run) { r.Head = core },
		"go test failed":            func(r *Run) { r.ExitCode = 1 },
		"failed native test log":    func(r *Run) { r.Log = read(t, "qualify/testdata/native-fail.log") },
		"short selection log":       func(r *Run) { r.Log = read(t, "qualify/testdata/native-install-pass.log") },
		"empty log":                 func(r *Run) { r.Log = nil },
		"unqualified host":          func(r *Run) { r.Host = "linux/amd64" },
		"wrong client revision":     func(r *Run) { r.SBXBefore = staleRevision },
		"daemon unavailable":        func(r *Run) { r.SBXAfter = unavailable },
		"sbx changed during run":    func(r *Run) { r.SBXAfter = staleRevision },
		"end before start":          func(r *Run) { r.Ended = r.Started },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			run := validRun(t)
			mutate(&run)
			if _, err := Build(profile, run); err == nil {
				t.Fatal("Build accepted a run it must refuse")
			}
		})
	}
}

func TestBuildRecordsLocalRun(t *testing.T) {
	run := validRun(t)
	host, err := Build(loadProfile(t), run)
	if err != nil {
		t.Fatal(err)
	}
	if host.LocalRun.LogSHA256 != SHA256Hex(run.Log) || host.LocalRun.Kind != LocalKind {
		t.Fatalf("local run record is wrong: %+v", host.LocalRun)
	}
	profile := loadProfile(t)
	if host.ClientVersion != profile.QualifiedSBXVersions[0] || host.ProviderCommit != candidate {
		t.Fatalf("host record is wrong: %+v", host)
	}
	if host.DockerServerVersion+host.BuildxVersion+host.ComposeVersion == "" {
		t.Fatalf("guest versions are missing: %+v", host)
	}
}

func TestVerifyAcceptsValidEvidence(t *testing.T) {
	if err := Verify(
		loadProfile(t),
		validEvidence(t),
		Expect{candidate, core, version},
	); err != nil {
		t.Fatal(err)
	}
}

func clone(t *testing.T, ev Evidence) Evidence {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var out Evidence
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifyRefusesTamperedEvidence(t *testing.T) {
	cases := map[string]func(*Evidence){
		"wrong candidate commit": func(e *Evidence) { e.CandidateCommit = core },
		"wrong core commit":      func(e *Evidence) { e.CoreCommit = candidate },
		"wrong provider version": func(e *Evidence) { e.ProviderVersion = unknown },
		"unqualified host":       func(e *Evidence) { e.Hosts[0].Host = "linux/amd64" },
		"extra host":             func(e *Evidence) { e.Hosts = append(e.Hosts, e.Hosts[0]) },
		"no hosts":               func(e *Evidence) { e.Hosts = nil },
		"wrong client revision":  func(e *Evidence) { e.Hosts[0].ClientRevision = "abc" },
		"wrong client version":   func(e *Evidence) { e.Hosts[0].ClientVersion = unknown },
		"wrong daemon version":   func(e *Evidence) { e.Hosts[0].DaemonVersion = unknown },
		"missing log sha":        func(e *Evidence) { e.Hosts[0].LocalRun.LogSHA256 = "" },
		"malformed log sha":      func(e *Evidence) { e.Hosts[0].LocalRun.LogSHA256 = "abc" },
		"missing host hash":      func(e *Evidence) { e.Hosts[0].LocalRun.HostSHA256 = "" },
		"not a local run":        func(e *Evidence) { e.Hosts[0].LocalRun.Kind = "ci" },
		"partial test selection": func(e *Evidence) { e.Hosts[0].LocalRun.Selection = "^TestNativeProviderInstallation$" },
		"required test absent":   func(e *Evidence) { e.Hosts[0].LocalRun.Tests = nil },
		"missing start time":     func(e *Evidence) { e.Hosts[0].LocalRun.StartedAt = "" },
		"provider commit":        func(e *Evidence) { e.Hosts[0].ProviderCommit = core },
		"gate not passed":        func(e *Evidence) { e.Hosts[0].Gates["network"] = "failed" },
		"gate absent":            func(e *Evidence) { delete(e.Hosts[0].Gates, "sizing") },
		"daemon schema":          func(e *Evidence) { e.Hosts[0].DaemonSchema = unknown + "/server-running" },
		"disk sizing schema":     func(e *Evidence) { e.Hosts[0].DiskSizingSchema = "" },
		"docker version":         func(e *Evidence) { e.Hosts[0].DockerServerVersion = "" },
		"provenance hash":        func(e *Evidence) { e.Hosts[0].FixtureProvenanceSHA256 = "x" },
		"host os version":        func(e *Evidence) { e.Hosts[0].HostOSVersion = "" },
	}
	profile := loadProfile(t)
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ev := clone(t, validEvidence(t))
			mutate(&ev)
			if err := Verify(profile, ev, Expect{candidate, core, version}); err == nil {
				t.Fatal("Verify accepted tampered evidence")
			}
		})
	}
}

func TestVerifyRefusesWrongExpectation(t *testing.T) {
	ev := validEvidence(t)
	profile := loadProfile(t)
	for name, want := range map[string]Expect{
		"candidate": {core, core, version},
		"core":      {candidate, candidate, version},
		"version":   {candidate, core, "v0.1.3"},
	} {
		if err := Verify(profile, ev, want); err == nil {
			t.Fatalf("Verify accepted a different %s", name)
		}
	}
}

func TestMergeReplacesHostAndRejectsOtherCandidate(t *testing.T) {
	ev := validEvidence(t)
	run := validRun(t)
	merged, err := Merge(&ev, run, ev.Hosts[0])
	if err != nil || len(merged.Hosts) != 1 {
		t.Fatalf("merge did not replace the host entry: %v %d", err, len(merged.Hosts))
	}
	run.Candidate = core
	if _, err := Merge(&ev, run, ev.Hosts[0]); err == nil {
		t.Fatal("merge accepted evidence for another candidate")
	}
}

func TestParseLogMarkers(t *testing.T) {
	log := "=== RUN x\n    w.go:1: native-evidence docker_server_version=1.2.3 " +
		"buildx_version=v4 compose_version=5.6\n--- PASS: TestNativeWorkspace (1s)\n" +
		"--- PASS: TestNativeProviderCrash (1s)\n--- PASS: TestNativeProviderInstallation (1s)\n" +
		"ok  \texample/integration\t3s\n"
	facts, err := ParseLog([]byte(log), false)
	if err != nil || facts.Docker != "1.2.3" || facts.Buildx != "v4" || facts.Compose != "5.6" {
		t.Fatalf("unexpected result: %+v %v", facts, err)
	}
	if _, err := ParseLog(
		[]byte(
			strings.Replace(
				log,
				"--- PASS: TestNativeProviderCrash",
				"--- SKIP: TestNativeProviderCrash",
				1,
			),
		),
		false,
	); err == nil {
		t.Fatal("a skipped required test passed")
	}
}

func TestPartialRunIsLabeledAndNeverVerifies(t *testing.T) {
	run := validRun(t)
	run.Partial = true
	run.Selection = "^TestNativeProviderInstallation$"
	run.Log = read(t, "qualify/testdata/native-install-pass.log")
	host, err := Build(loadProfile(t), run)
	if err != nil {
		t.Fatal(err)
	}
	if host.LocalRun.Kind != TestOutputKind || host.Gates["network"] != "not_run" {
		t.Fatalf("partial run is not labeled: %+v", host)
	}
	ev, err := Merge(nil, run, host)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(loadProfile(t), ev, Expect{candidate, core, version}); err == nil {
		t.Fatal("Verify accepted test output as evidence")
	}
}

func TestParseTupleRecordedFixtures(t *testing.T) {
	profile := loadProfile(t)
	for _, client := range profile.QualifiedSBXVersions {
		path := "../sbx/testdata/" + client + "/version-daemon-running.json"
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if _, err := ParseTuple(profile, read(t, path)); err != nil {
			t.Fatalf("%s: %v", client, err)
		}
	}
}
