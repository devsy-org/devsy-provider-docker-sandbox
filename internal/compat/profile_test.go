package compat

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	betaMinimum      = "v1.27.0-beta.4"
	unmeasured       = "unmeasured"
	otherHost        = "plan9/mips"
	measuredVersion  = "v0.50.1"
	measuredRevision = "measured"
	newerVersion     = "v0.51.0"
)

func TestUnqualifiedProfileFailsClosed(t *testing.T) {
	p, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if p.MinimumDevsyVersion != "" || len(p.QualifiedHosts) != 0 {
		t.Skip("release profile qualified externally")
	}
	if e = p.Devsy(context.Background(), ""); e == nil {
		t.Fatal("unqualified Devsy admitted")
	}
	if e = p.RequireInventorySchema(); e == nil {
		t.Fatal("unqualified inventory admitted")
	}
}

func TestSemverStrict(t *testing.T) {
	for _, v := range []string{
		"v0.0", "v01.2.3", "garbage", "v1.2.3-", "v1.2.3-beta..1",
		"v1.2.3-01", "v1.2.3-be_ta", "v1.2.3+build",
	} {
		if _, e := parseSemver(v); e == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestSemverPrecedence(t *testing.T) {
	ordered := []string{
		"v1.0.0-alpha",
		"v1.0.0-alpha.1",
		"v1.0.0-alpha.beta",
		"v1.0.0-beta",
		"v1.0.0-beta.2",
		"v1.0.0-beta.11",
		"v1.0.0-rc.1",
		"v1.0.0",
		"v1.0.1",
		"v1.1.0",
		"v2.0.0",
	}
	for i, av := range ordered {
		a, e := parseSemver(av)
		if e != nil {
			t.Fatal(e)
		}
		for j, bv := range ordered {
			b, e := parseSemver(bv)
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := compareSemver(a, b); got != want {
				t.Fatalf("compare(%s, %s) = %d, want %d", av, bv, got, want)
			}
		}
	}
}

func TestDevsyVersionAdmission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test Devsy executable uses a POSIX shell")
	}
	for _, tc := range []struct {
		name, minimum, reported string
		admitted                bool
	}{
		{"prerelease equal to prerelease minimum", betaMinimum, betaMinimum, true},
		{"later prerelease", betaMinimum, "v1.27.0-beta.5", true},
		{"stable after prerelease minimum", betaMinimum, "v1.27.0", true},
		{"earlier prerelease", betaMinimum, "v1.27.0-beta.3", false},
		{"prerelease of stable minimum", "v1.27.0", betaMinimum, false},
		{"development build", betaMinimum, "v0.0.0-dev", false},
		{"older stable", betaMinimum, "v1.26.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devsy := filepath.Join(t.TempDir(), "devsy")
			root, err := os.OpenRoot(filepath.Dir(devsy))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := root.WriteFile(
				"devsy",
				[]byte("#!/bin/sh\nprintf '"+tc.reported+"\\n'\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			err = (Profile{MinimumDevsyVersion: tc.minimum}).Devsy(context.Background(), devsy)
			if (err == nil) != tc.admitted {
				t.Fatalf("Devsy %s against minimum %s: %v", tc.reported, tc.minimum, err)
			}
		})
	}
}

func TestUnqualifiedHostIsAdmittedWithWarning(t *testing.T) {
	p := Profile{QualifiedHosts: []string{otherHost}}
	if p.HostQualified() {
		t.Fatal("host reported as natively qualified")
	}
	if err := p.Host(); err != nil {
		t.Fatalf("unqualified host refused: %v", err)
	}
	if err := p.RequireDiskSizingQualification(); err != nil {
		t.Fatalf("unqualified host refused for disk sizing: %v", err)
	}
	warning := p.HostWarning()
	for _, want := range []string{
		runtime.GOOS + "/" + runtime.GOARCH,
		"built and unit-tested only",
		"no native qualification evidence",
		otherHost,
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning %q lacks %q", warning, want)
		}
	}
	if warning = (Profile{}).HostWarning(); !strings.Contains(warning, "none") {
		t.Fatalf("warning for an empty profile = %q", warning)
	}
}

func TestQualifiedHostDoesNotWarn(t *testing.T) {
	p := Profile{QualifiedHosts: []string{runtime.GOOS + "/" + runtime.GOARCH, otherHost}}
	if !p.HostQualified() {
		t.Fatal("qualified host not recognized")
	}
	if warning := p.HostWarning(); warning != "" {
		t.Fatalf("qualified host warned: %q", warning)
	}
}

func TestQualifiedHostDiskCapacityFailsClosed(t *testing.T) {
	p := Profile{QualifiedHosts: []string{runtime.GOOS + "/" + runtime.GOARCH}}
	err := p.RequireDiskSizingQualification()
	tagged, ok := err.(*Error)
	if !ok || tagged.Code != "E_GUEST_CAPABILITY" {
		t.Fatalf("unmeasured disk overhead accepted: %v", err)
	}
}

func measuredProfile(t *testing.T) Profile {
	t.Helper()
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMeasuredSchemaGates(t *testing.T) {
	p := measuredProfile(t)
	if err := p.RequireShellSchema(); err != nil {
		t.Fatal(err)
	}
	if err := p.RequireOperationResultSchema(); err != nil {
		t.Fatal(err)
	}
	p.ShellSettingSchema = unmeasured
	if err := p.RequireShellSchema(); err == nil {
		t.Fatal("unmeasured shell schema accepted")
	}
	p.OperationResultSchema = unmeasured
	if err := p.RequireOperationResultSchema(); err == nil {
		t.Fatal("unmeasured result schema accepted")
	}
}

func TestMeasuredDiskSizingOnQualifiedHost(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("measured disk sizing is native to darwin/arm64")
	}
	p := measuredProfile(t)
	if err := p.RequireDiskSizingQualification(); err != nil {
		t.Fatal(err)
	}
	p.QualifiedSBXRevisions[p.QualifiedSBXVersions[0]] = unmeasured
	if err := p.RequireDiskSizingQualification(); err == nil {
		t.Fatal("unmeasured client revision accepted")
	}
}

func tupleProfile() Profile {
	return Profile{
		QualifiedSBXVersions:    []string{measuredVersion},
		QualifiedSBXRevisions:   map[string]string{measuredVersion: measuredRevision},
		QualifiedDaemonVersions: map[string][]string{measuredVersion: {measuredVersion}},
	}
}

func TestTupleQualification(t *testing.T) {
	p := tupleProfile()
	for _, test := range []struct {
		name      string
		tuple     Tuple
		qualified bool
	}{
		{"qualified tuple", Tuple{measuredVersion, measuredRevision, measuredVersion}, true},
		{"newer client", Tuple{newerVersion, measuredRevision, measuredVersion}, false},
		{"other revision", Tuple{measuredVersion, "other", measuredVersion}, false},
		{"other daemon", Tuple{measuredVersion, measuredRevision, newerVersion}, false},
		{"no revision", Tuple{measuredVersion, "", measuredVersion}, false},
		{"no daemon", Tuple{measuredVersion, measuredRevision, ""}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := p.TupleQualified(test.tuple); got != test.qualified {
				t.Fatalf("TupleQualified(%+v) = %t", test.tuple, got)
			}
			if warning := p.TupleWarning(test.tuple); (warning == "") != test.qualified {
				t.Fatalf("TupleWarning(%+v) = %q", test.tuple, warning)
			}
		})
	}
}

func TestReleaseProfileTupleBoundaries(t *testing.T) {
	p := measuredProfile(t)
	version := p.QualifiedSBXVersions[0]
	qualified := Tuple{
		version,
		p.QualifiedSBXRevisions[version],
		p.QualifiedDaemonVersions[version][0],
	}
	if !p.TupleQualified(qualified) || p.TupleWarning(qualified) != "" {
		t.Fatal("qualified release tuple warned")
	}
	for _, tuple := range []Tuple{
		{measuredVersion, "ddb0268d7124b9cf98955d0b08ffd71a0dfcebe4", measuredVersion},
		{"v0.53.0-rc2", "unqualified", "v0.53.0-rc2"},
		{version, "unqualified", qualified.Daemon},
		{version, qualified.Revision, measuredVersion},
	} {
		if p.TupleQualified(tuple) || !strings.Contains(p.TupleWarning(tuple), "runs best effort") {
			t.Fatalf("unqualified release tuple did not warn: %+v", tuple)
		}
		if MeasuredDiskSizingSchema("darwin/arm64", tuple.Client, tuple.Revision) != "" &&
			tuple.Client != qualified.Client {
			t.Fatalf("unqualified client has measured disk sizing: %+v", tuple)
		}
	}
}

func TestTupleWarningNamesBothVersionsAndIssueTracker(t *testing.T) {
	warning := tupleProfile().TupleWarning(Tuple{newerVersion, "", newerVersion})
	for _, want := range []string{
		"sbx v0.51.0 revision unknown daemon v0.51.0",
		"sbx v0.50.1 revision measured daemon v0.50.1",
		"file an issue",
		IssuesURL,
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning %q lacks %q", warning, want)
		}
	}
	if !strings.Contains((Profile{}).TupleWarning(Tuple{"v1", "r", "v1"}), "qualified: none") {
		t.Fatal("empty profile warning does not say nothing is qualified")
	}
}

func TestWarnerEmitsEachMessageOnce(t *testing.T) {
	var out strings.Builder
	w := NewWarner(&out)
	w.Warn("first")
	w.Warn("first")
	w.Warn("second")
	w.Warn("")
	if out.String() != "warning: first\nwarning: second\n" {
		t.Fatalf("warnings = %q", out.String())
	}
	var disabled *Warner
	disabled.Warn("ignored")
}
