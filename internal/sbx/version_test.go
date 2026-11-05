package sbx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func TestObservedUnavailableDaemonIsNotHealthy(t *testing.T) {
	data, e := os.ReadFile(
		filepath.Join("testdata", testVersion, "version-daemon-unavailable.json"),
	)
	if e != nil {
		t.Fatal(e)
	}
	var v Version
	if e = exact(data, &v); e != nil {
		t.Fatal(e)
	}
	if v.Client.Version != testVersion ||
		v.Client.Revision != "ddb0268d7124b9cf98955d0b08ffd71a0dfcebe4" {
		t.Fatalf("observed version changed: %+v", v.Client)
	}
	p := compat.Profile{
		DaemonSchema:            "test-observed-unavailable",
		QualifiedDaemonVersions: map[string][]string{testVersion: {testVersion}},
	}
	if _, e = ServerVersion(v, p); e == nil {
		t.Fatal("unavailable daemon accepted as healthy")
	}
}

func TestObservedRunningDaemonVersion(t *testing.T) {
	var observed Version
	if err := exact(readMeasuredFixture(t, "version-daemon-running.json"), &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Client.Version != testVersion ||
		observed.Client.Revision != "ddb0268d7124b9cf98955d0b08ffd71a0dfcebe4" {
		t.Fatalf("unexpected measured sbx client: %+v", observed.Client)
	}
	profile := compat.Profile{
		DaemonSchema:            "v0.50.1/server-running",
		QualifiedDaemonVersions: map[string][]string{testVersion: {testVersion}},
	}
	if version, err := ServerVersion(observed, profile); err != nil || version != testVersion {
		t.Fatalf("measured daemon version = %q, %v", version, err)
	}
}

func TestObservedLoginErrorRequiresExactMatch(t *testing.T) {
	data, e := os.ReadFile(filepath.Join("testdata", testVersion, "inventory-not-signed-in.stderr"))
	if e != nil {
		t.Fatal(e)
	}
	if !observedLoginFailure(data) {
		t.Fatal("recorded login error not recognized")
	}
	if !observedLoginFailure([]byte("error: not signed in to Docker\r\n  try: sbx login\r\n")) {
		t.Fatal("Windows line endings changed observed authentication result")
	}
	if observedLoginFailure([]byte("not signed in")) {
		t.Fatal("unqualified text misclassified as authentication")
	}
}
