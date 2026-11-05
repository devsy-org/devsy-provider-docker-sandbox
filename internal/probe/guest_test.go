package probe

import (
	"fmt"
	"testing"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
)

func TestMinimumCapacity(t *testing.T) {
	for _, tc := range []struct {
		requested int64
		minimum   int64
	}{
		{1000, 970},
		{999, 970},
		{24 << 30, 24996709663},
		{12 << 30, 12498354832},
	} {
		if got := minimumCapacity(tc.requested); got != tc.minimum {
			t.Errorf("minimumCapacity(%d) = %d, want %d", tc.requested, got, tc.minimum)
		}
	}
}

func TestDiskCapacityMatch(t *testing.T) {
	requirement := diskRequirement{rootBytes: 1000, dockerBytes: 2000}
	for _, tc := range []struct {
		name    string
		devices [2]int64
		root    [2]int64
		docker  [2]int64
		want    bool
	}{
		{"requested capacities", [2]int64{101, 202}, [2]int64{1000, 1}, [2]int64{2000, 1}, true},
		{"minimum capacities", [2]int64{101, 202}, [2]int64{970, 1}, [2]int64{1940, 1}, true},
		{"undersized root", [2]int64{101, 202}, [2]int64{969, 1}, [2]int64{2000, 1}, false},
		{"undersized Docker", [2]int64{101, 202}, [2]int64{1000, 1}, [2]int64{1939, 1}, false},
		{"oversized root", [2]int64{101, 202}, [2]int64{1001, 1}, [2]int64{2000, 1}, false},
		{"oversized Docker", [2]int64{101, 202}, [2]int64{1000, 1}, [2]int64{2001, 1}, false},
		{"swapped sizes", [2]int64{101, 202}, [2]int64{2000, 1}, [2]int64{1000, 1}, false},
		{"shared filesystem", [2]int64{101, 101}, [2]int64{1000, 1}, [2]int64{2000, 1}, false},
		{"no root available", [2]int64{101, 202}, [2]int64{1000, 0}, [2]int64{2000, 1}, false},
		{"no Docker available", [2]int64{101, 202}, [2]int64{1000, 1}, [2]int64{2000, 0}, false},
		{"invalid root available", [2]int64{101, 202}, [2]int64{1000, 1001}, [2]int64{2000, 1}, false},
		{"invalid Docker available", [2]int64{101, 202}, [2]int64{1000, 1}, [2]int64{2000, 2001}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := fmt.Sprintf("%d %d\n%d %d\n%d %d\n", tc.devices[0], tc.devices[1], tc.root[0], tc.root[1], tc.docker[0], tc.docker[1])
			if got := matchDiskCapacity(requirement, []byte(output)); got != tc.want {
				t.Errorf("matchDiskCapacity(%q) = %t, want %t", output, got, tc.want)
			}
		})
	}
	for _, output := range []string{"", "101 202\n1000 1\n", "101 202\n1000 1\n2000 1\nextra", "101 202\n1000 unknown\n2000 1\n"} {
		if matchDiskCapacity(requirement, []byte(output)) {
			t.Errorf("invalid disk report accepted: %q", output)
		}
	}
	for _, invalid := range []diskRequirement{{rootBytes: 0, dockerBytes: 2000}, {rootBytes: 1000, dockerBytes: 0}} {
		if matchDiskCapacity(invalid, []byte("101 202\n1000 1\n2000 1\n")) {
			t.Errorf("invalid disk requirement accepted: %+v", invalid)
		}
	}
}

func TestMeasuredGuestDiskProbe(t *testing.T) {
	spec := config.Spec{RootDiskBytes: 24 << 30, DockerDiskBytes: 12 << 30}
	checks := Checks(spec)
	check := checks[len(checks)-1]
	if err := Match(check, []byte("101 202\n25185042432 1\n12573614080 1\n")); err != nil {
		t.Fatalf("measured guest capacities rejected: %v", err)
	}
	if err := Match(check, []byte("101 202\n20957446144 1\n10464022528 1\n")); err == nil {
		t.Fatal("default guest capacities accepted for larger requested disks")
	}
	if err := Match(check, []byte("101 202\n25769803777 1\n12573614080 1\n")); err == nil {
		t.Fatal("oversized root filesystem accepted")
	}
	if err := Match(Checks(config.Spec{})[len(checks)-1], []byte("101 202\n25185042432 1\n12573614080 1\n")); err == nil {
		t.Fatal("zero requested disk capacity accepted")
	}
}
