package compat

import (
	"context"
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
	for _, v := range []string{"v0.0", "v0.0.0-dev", "v01.2.3", "garbage"} {
		if _, e := parseSemver(v); e == nil {
			t.Fatalf("accepted %s", v)
		}
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
