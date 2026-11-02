package compat

import (
	"context"
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
