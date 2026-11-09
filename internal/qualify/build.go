package qualify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

type sbxClient struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

type sbxServer struct {
	State   string `json:"state"`
	Version string `json:"version"`
}

type sbxVersion struct {
	Client sbxClient `json:"client"`
	Server sbxServer `json:"server"`
}

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ParseTuple(profile Profile, data []byte) (Tuple, error) {
	var v sbxVersion
	if err := json.Unmarshal(data, &v); err != nil {
		return Tuple{}, fmt.Errorf("unreadable sbx version output: %w", err)
	}
	if v.Server.State != "running" {
		return Tuple{}, fmt.Errorf("sbx daemon state is %q, want running", v.Server.State)
	}
	t := Tuple{Client: v.Client.Version, Revision: v.Client.Revision, Daemon: v.Server.Version}
	if !profile.TupleQualified(t) {
		return t, fmt.Errorf(
			"sbx client %s revision %s daemon %s does not match the qualified profile",
			t.Client, t.Revision, t.Daemon,
		)
	}
	return t, nil
}

func checkSource(r Run) error {
	var errs []error
	if r.Dirty {
		errs = append(errs, errors.New("working tree is dirty"))
	}
	if r.Head != r.Candidate {
		errs = append(errs, fmt.Errorf("HEAD %s is not candidate %s", r.Head, r.Candidate))
	}
	if r.ExitCode != 0 {
		errs = append(errs, fmt.Errorf("native tests exited with status %d", r.ExitCode))
	}
	if !r.Ended.After(r.Started) {
		errs = append(errs, errors.New("run end time is not after the start time"))
	}
	return errors.Join(errs...)
}

func checkSBX(profile Profile, r Run) (Tuple, error) {
	before, err := ParseTuple(profile, r.SBXBefore)
	if err != nil {
		return before, fmt.Errorf("before the run: %w", err)
	}
	after, err := ParseTuple(profile, r.SBXAfter)
	if err != nil {
		return before, fmt.Errorf("after the run: %w", err)
	}
	if before != after {
		return before, errors.New("sbx client or daemon changed during the run")
	}
	return before, nil
}

func Build(profile Profile, r Run) (Host, error) {
	if !slices.Contains(profile.QualifiedHosts, r.Host) {
		return Host{}, fmt.Errorf("host %s is not listed in qualified_hosts", r.Host)
	}
	tuple, tupleErr := checkSBX(profile, r)
	facts, logErr := ParseLog(r.Log, r.Partial)
	if err := errors.Join(checkSource(r), tupleErr, logErr); err != nil {
		return Host{}, err
	}
	gates := make(map[string]string, len(Gates))
	for _, gate := range Gates {
		gates[gate] = gateResult(r)
	}
	return Host{
		Host:                    r.Host,
		ClientVersion:           tuple.Client,
		ClientRevision:          tuple.Revision,
		DaemonVersion:           tuple.Daemon,
		DaemonSchema:            profile.DaemonSchema,
		OperationResultSchema:   profile.OperationResultSchema,
		ShellSettingSchema:      profile.ShellSettingSchema,
		DiskSizingSchema:        profile.DiskSizingSchemas[r.Host],
		DockerServerVersion:     facts.Docker,
		BuildxVersion:           facts.Buildx,
		ComposeVersion:          facts.Compose,
		HostOSVersion:           r.HostOSVersion,
		ProviderCommit:          r.Candidate,
		FixtureProvenanceSHA256: r.ProvenanceSHA256,
		Gates:                   gates,
		LocalRun: LocalRun{
			Kind:       kind(r),
			LogSHA256:  SHA256Hex(r.Log),
			StartedAt:  r.Started.UTC().Format(time.RFC3339),
			EndedAt:    r.Ended.UTC().Format(time.RFC3339),
			HostSHA256: SHA256Hex([]byte(r.Hostname)),
			Selection:  r.Selection,
			Tests:      facts.Passed,
		},
	}, nil
}

func Merge(existing *Evidence, r Run, host Host) (Evidence, error) {
	ev := Evidence{
		CandidateCommit: r.Candidate,
		CoreCommit:      r.CoreCommit,
		ProviderVersion: r.Version,
	}
	if existing != nil {
		if existing.CandidateCommit != r.Candidate || existing.CoreCommit != r.CoreCommit ||
			existing.ProviderVersion != r.Version {
			return ev, errors.New("existing evidence is for a different candidate, core or version")
		}
		for _, h := range existing.Hosts {
			if h.Host != host.Host {
				ev.Hosts = append(ev.Hosts, h)
			}
		}
	}
	ev.Hosts = append(ev.Hosts, host)
	slices.SortFunc(
		ev.Hosts,
		func(a, b Host) int { return bytes.Compare([]byte(a.Host), []byte(b.Host)) },
	)
	return ev, nil
}

func kind(r Run) string {
	if r.Partial {
		return TestOutputKind
	}
	return LocalKind
}

func gateResult(r Run) string {
	if r.Partial {
		return "not_run"
	}
	return "passed"
}
