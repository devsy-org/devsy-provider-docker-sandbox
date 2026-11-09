package qualify

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

var (
	hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func Verify(profile Profile, ev Evidence, want Expect) error {
	var errs []error
	add := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	add(
		ev.CandidateCommit == want.Candidate,
		"candidate_commit %q is not %s",
		ev.CandidateCommit,
		want.Candidate,
	)
	add(
		ev.CoreCommit == want.CoreCommit,
		"core_commit %q is not %s",
		ev.CoreCommit,
		want.CoreCommit,
	)
	add(
		ev.ProviderVersion == want.Version,
		"provider_version %q is not %s",
		ev.ProviderVersion,
		want.Version,
	)
	hosts := make([]string, 0, len(ev.Hosts))
	for _, h := range ev.Hosts {
		hosts = append(hosts, h.Host)
	}
	slices.Sort(hosts)
	qualified := slices.Sorted(slices.Values(profile.QualifiedHosts))
	add(
		slices.Equal(hosts, qualified),
		"evidence hosts %v differ from qualified_hosts %v",
		hosts,
		qualified,
	)
	for _, h := range ev.Hosts {
		errs = append(errs, verifyHost(profile, ev, h)...)
	}
	return errors.Join(errs...)
}

func verifyHost(profile Profile, ev Evidence, h Host) []error {
	var errs []error
	prefix := h.Host + ": "
	for _, err := range slices.Concat(
		verifyTuple(profile, h),
		verifySchemas(profile, h),
		verifyFields(ev, h),
		verifyGates(h),
		verifyRun(h.LocalRun),
	) {
		errs = append(errs, errors.New(prefix+err.Error()))
	}
	return errs
}

func verifyTuple(profile Profile, h Host) []error {
	t := Tuple{Client: h.ClientVersion, Revision: h.ClientRevision, Daemon: h.DaemonVersion}
	if profile.TupleQualified(t) {
		return nil
	}
	return []error{fmt.Errorf(
		"sbx client %q revision %q daemon %q is not in the qualified profile",
		t.Client, t.Revision, t.Daemon,
	)}
}

func verifySchemas(profile Profile, h Host) []error {
	var errs []error
	for _, c := range []struct{ name, got, want string }{
		{"daemon_schema", h.DaemonSchema, profile.DaemonSchema},
		{"operation_result_schema", h.OperationResultSchema, profile.OperationResultSchema},
		{"shell_setting_schema", h.ShellSettingSchema, profile.ShellSettingSchema},
		{"disk_sizing_schema", h.DiskSizingSchema, profile.DiskSizingSchemas[h.Host]},
	} {
		if c.got == "" || c.got != c.want {
			errs = append(errs, fmt.Errorf("%s %q is not %q", c.name, c.got, c.want))
		}
	}
	return errs
}

func verifyFields(ev Evidence, h Host) []error {
	var errs []error
	if h.ProviderCommit != ev.CandidateCommit {
		errs = append(errs, fmt.Errorf("provider_commit %q is not the candidate", h.ProviderCommit))
	}
	if !hex64.MatchString(h.FixtureProvenanceSHA256) {
		errs = append(errs, errors.New("fixture_provenance_sha256 is not a sha256"))
	}
	for name, value := range map[string]string{
		"host_os_version":       h.HostOSVersion,
		"docker_server_version": h.DockerServerVersion,
		"buildx_version":        h.BuildxVersion,
		"compose_version":       h.ComposeVersion,
	} {
		if value == "" {
			errs = append(errs, fmt.Errorf("%s is empty", name))
		}
	}
	return errs
}

func verifyGates(h Host) []error {
	var errs []error
	for _, gate := range Gates {
		if h.Gates[gate] != "passed" {
			errs = append(errs, fmt.Errorf("gate %s is %q, want passed", gate, h.Gates[gate]))
		}
	}
	return errs
}

func verifyRun(r LocalRun) []error {
	var errs []error
	if r.Kind != LocalKind {
		errs = append(errs, fmt.Errorf("local_run.kind %q is not %s", r.Kind, LocalKind))
	}
	if !hex64.MatchString(r.LogSHA256) {
		errs = append(errs, errors.New("local_run.log_sha256 is missing or not a sha256"))
	}
	if !hex64.MatchString(r.HostSHA256) {
		errs = append(errs, errors.New("local_run.host_sha256 is missing or not a sha256"))
	}
	if r.Selection != Selection {
		errs = append(
			errs,
			fmt.Errorf("local_run.selection %q is not the full suite %s", r.Selection, Selection),
		)
	}
	for _, name := range RequiredTests {
		if !slices.Contains(r.Tests, name) {
			errs = append(errs, fmt.Errorf("local_run.tests lacks %s", name))
		}
	}
	return append(errs, verifyTimes(r)...)
}

func verifyTimes(r LocalRun) []error {
	start, err1 := time.Parse(time.RFC3339, r.StartedAt)
	end, err2 := time.Parse(time.RFC3339, r.EndedAt)
	if err1 != nil || err2 != nil || !end.After(start) {
		return []error{errors.New("local_run times are missing, unreadable or out of order")}
	}
	return nil
}

func ValidCommit(s string) bool { return sha40.MatchString(s) }
