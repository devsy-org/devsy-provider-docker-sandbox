package probe

import (
	"fmt"
	"strings"
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
			output := fmt.Sprintf(
				"%d %d\n%d %d\n%d %d\n",
				tc.devices[0],
				tc.devices[1],
				tc.root[0],
				tc.root[1],
				tc.docker[0],
				tc.docker[1],
			)
			if got := matchDiskCapacity(requirement, []byte(output)) == nil; got != tc.want {
				t.Errorf("matchDiskCapacity(%q) = %t, want %t", output, got, tc.want)
			}
		})
	}
	for _, output := range []string{
		"", "101 202\n1000 1\n", "101 202\n1000 1\n2000 1\nextra",
		"101 202\n1000 unknown\n2000 1\n",
	} {
		if matchDiskCapacity(requirement, []byte(output)) == nil {
			t.Errorf("invalid disk report accepted: %q", output)
		}
	}
	for _, invalid := range []diskRequirement{{rootBytes: 0, dockerBytes: 2000}, {rootBytes: 1000, dockerBytes: 0}} {
		if matchDiskCapacity(invalid, []byte("101 202\n1000 1\n2000 1\n")) == nil {
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
	if err := Match(
		Checks(config.Spec{})[len(checks)-1],
		[]byte("101 202\n25185042432 1\n12573614080 1\n"),
	); err == nil {
		t.Fatal("zero requested disk capacity accepted")
	}
}

func TestQualifiedDockerDiskMeasurementsV0501Darwin(t *testing.T) {
	root := int64(8350298112)
	cases := []struct {
		requestedMiB int64
		measured     int64
		accepted     bool
	}{
		{512, 510873600, false},
		{1024, 1020702720, false},
		{2048, 2040373248, false},
		{4096, 4143677440, false},
		{6144, 6249086976, false},
		{6656, 6772027392, true},
		{7168, 7298117632, true},
		{7680, 7824207872, true},
		{8192, 8350298112, true},
		{9216, 9407123456, true},
		{16384, 16729894912, true},
		{40960, 41956900864, true},
	}
	for _, tt := range cases {
		spec := config.Spec{RootDiskBytes: 8 << 30, DockerDiskBytes: tt.requestedMiB << 20}
		checks := Checks(spec)
		data := fmt.Appendf(nil, "29 65072\n%d 1\n%d 1\n", root, tt.measured)
		err := Match(checks[len(checks)-1], data)
		if (err == nil) != tt.accepted {
			t.Errorf(
				"%dMiB measured %d: accepted=%v want %v",
				tt.requestedMiB,
				tt.measured,
				err == nil,
				tt.accepted,
			)
		}
	}
}

func TestCapacityShortfallErrorNamesDiskAndSuggestsLargerSize(t *testing.T) {
	cases := []struct {
		name         string
		rootMiB      int64
		dockerMiB    int64
		root, docker int64
		want         []string
	}{
		{
			"docker disk 6144MiB measured on v0.50.1 darwin/arm64",
			8192,
			6144,
			8350298112,
			6249086976,
			[]string{
				"Docker disk (SBX_DOCKER_DISK)",
				"requested 6442450944 bytes (6.00 GiB)",
				"guest reports 6249086976 bytes (5.82 GiB)",
				"3.00 percent short",
				"such as 8g may pass",
			},
		},
		{
			"root disk", 8192, 8192, 8000000000, 8350298112,
			[]string{
				"root disk (SBX_ROOT_DISK)", "requested 8589934592 bytes (8.00 GiB)",
				"guest reports 8000000000 bytes", "6.87 percent short", "such as 9g may pass",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := config.Spec{
				RootDiskBytes:   tc.rootMiB << 20,
				DockerDiskBytes: tc.dockerMiB << 20,
			}
			checks := Checks(spec)
			data := fmt.Appendf(nil, "29 65072\n%d 1\n%d 1\n", tc.root, tc.docker)
			err := Match(checks[len(checks)-1], data)
			if err == nil {
				t.Fatal("undersized disk accepted")
			}
			for _, fragment := range tc.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error %q lacks %q", err, fragment)
				}
			}
			if strings.Contains(err.Error(), "will pass") ||
				strings.Contains(err.Error(), "guarantee") {
				t.Errorf("error promises success: %q", err)
			}
		})
	}
}

func TestCapacityErrorStaysGenericForNonShortfalls(t *testing.T) {
	spec := config.Spec{RootDiskBytes: 8 << 30, DockerDiskBytes: 8 << 30}
	checks := Checks(spec)
	for _, data := range []string{
		"29 65072\n8589934593 1\n8589934592 1\n",
		"29 29\n8589934592 1\n8589934592 1\n",
		"29 65072\n8589934592 9589934592\n8589934592 1\n",
	} {
		err := Match(checks[len(checks)-1], []byte(data))
		if err == nil || strings.Contains(err.Error(), "percent short") {
			t.Errorf("%q: got %v, want generic failure", data, err)
		}
	}
}
