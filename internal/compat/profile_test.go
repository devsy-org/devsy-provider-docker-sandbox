package compat

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnqualifiedProfileFailsClosed(t *testing.T) {
	p, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if p.MinimumDevsyVersion != "" || len(p.QualifiedHosts) != 0 {
		t.Skip("release profile qualified externally")
	}
	if e = p.Host(); e == nil {
		t.Fatal("unqualified host admitted")
	}
	if e = p.Devsy(context.Background(), ""); e == nil {
		t.Fatal("unqualified Devsy admitted")
	}
	if e = p.RequireInventorySchema(); e == nil {
		t.Fatal("unqualified inventory admitted")
	}
}
func TestSemverStrict(t *testing.T) {
	for _, v := range []string{"v0.0", "v01.2.3", "garbage", "v1.2.3-", "v1.2.3-beta..1", "v1.2.3-01", "v1.2.3-be_ta", "v1.2.3+build"} {
		if _, e := parseSemver(v); e == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestSemverPrecedence(t *testing.T) {
	ordered := []string{"v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta", "v1.0.0-beta.2", "v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0", "v1.0.1", "v1.1.0", "v2.0.0"}
	for i := range ordered {
		a, e := parseSemver(ordered[i])
		if e != nil {
			t.Fatal(e)
		}
		for j := range ordered {
			b, e := parseSemver(ordered[j])
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
				t.Fatalf("compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
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
		{"prerelease equal to prerelease minimum", "v1.27.0-beta.4", "v1.27.0-beta.4", true},
		{"later prerelease", "v1.27.0-beta.4", "v1.27.0-beta.5", true},
		{"stable after prerelease minimum", "v1.27.0-beta.4", "v1.27.0", true},
		{"earlier prerelease", "v1.27.0-beta.4", "v1.27.0-beta.3", false},
		{"prerelease of stable minimum", "v1.27.0", "v1.27.0-beta.4", false},
		{"development build", "v1.27.0-beta.4", "v0.0.0-dev", false},
		{"older stable", "v1.27.0-beta.4", "v1.26.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devsy := filepath.Join(t.TempDir(), "devsy")
			if err := os.WriteFile(devsy, []byte("#!/bin/sh\nprintf '"+tc.reported+"\\n'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			err := (Profile{MinimumDevsyVersion: tc.minimum}).Devsy(context.Background(), devsy)
			if (err == nil) != tc.admitted {
				t.Fatalf("Devsy %s against minimum %s: %v", tc.reported, tc.minimum, err)
			}
		})
	}
}
func TestUnmeasuredDiskCapacityFailsClosed(t *testing.T) {
	err := (Profile{}).RequireDiskSizingQualification()
	tagged, ok := err.(*Error)
	if !ok || tagged.Code != "E_GUEST_CAPABILITY" {
		t.Fatalf("unmeasured disk overhead accepted: %v", err)
	}
}

func TestMeasuredSchemaGates(t *testing.T) {
	p := Profile{
		ShellSettingSchema:    ShellSettingSchemaV0501,
		OperationResultSchema: OperationResultSchemaV0501,
		QualifiedSBXVersions:  []string{"v0.50.1"},
		QualifiedSBXRevisions: map[string]string{"v0.50.1": "ddb0268d7124b9cf98955d0b08ffd71a0dfcebe4"},
		DiskSizingSchemas:     map[string]string{"darwin/arm64": DiskSizingSchemaDarwinARM64V0501},
	}
	if err := p.RequireShellSchema(); err != nil {
		t.Fatal(err)
	}
	if err := p.RequireOperationResultSchema(); err != nil {
		t.Fatal(err)
	}
	err := p.RequireDiskSizingQualification()
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if err != nil {
			t.Fatal(err)
		}
	} else if err == nil {
		t.Fatal("unmeasured host accepted")
	}
	p.QualifiedSBXRevisions["v0.50.1"] = "unmeasured"
	if err := p.RequireDiskSizingQualification(); err == nil {
		t.Fatal("unmeasured client revision accepted")
	}
	p.ShellSettingSchema = "unmeasured"
	if err := p.RequireShellSchema(); err == nil {
		t.Fatal("unmeasured shell schema accepted")
	}
	p.OperationResultSchema = "unmeasured"
	if err := p.RequireOperationResultSchema(); err == nil {
		t.Fatal("unmeasured result schema accepted")
	}
}
