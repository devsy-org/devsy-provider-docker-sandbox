package qualify

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Facts struct {
	Docker  string
	Buildx  string
	Compose string
	Passed  []string
}

var (
	topLevel = regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (Test\w+) `)
	anyLevel = regexp.MustCompile(`^\s*--- (FAIL|SKIP): `)
	marker   = regexp.MustCompile(`\b(docker_server_version|buildx_version|compose_version)=(\S+)`)
	okLine   = regexp.MustCompile(`^ok\s+\S+\s+\S+`)
)

func ParseLog(log []byte, partial bool) (Facts, error) {
	var f Facts
	errs, passed, ok := f.scan(log)
	if !ok {
		errs = append(errs, errors.New("native test log has no package ok line"))
	}
	f.Passed = sortedKeys(passed)
	if partial {
		if len(passed) == 0 {
			errs = append(errs, errors.New("native test log has no passing test"))
		}
		return f, errors.Join(errs...)
	}
	return f, errors.Join(append(errs, f.requireFull(passed)...)...)
}

func (f *Facts) requireFull(passed map[string]bool) []error {
	var errs []error
	for _, name := range RequiredTests {
		if !passed[name] {
			errs = append(errs, fmt.Errorf("native test log has no passing %s", name))
		}
	}
	if f.Docker == "" || f.Buildx == "" || f.Compose == "" {
		errs = append(
			errs,
			errors.New("native test log lacks guest docker, buildx or compose versions"),
		)
	}
	return errs
}

func (f *Facts) scan(log []byte) (errs []error, passed map[string]bool, ok bool) {
	passed = map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(log))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if anyLevel.MatchString(line) {
			errs = append(errs, fmt.Errorf("native test log contains %q", strings.TrimSpace(line)))
		}
		if m := topLevel.FindStringSubmatch(line); m != nil && m[1] == "PASS" {
			passed[m[2]] = true
		}
		f.record(line)
		ok = ok || okLine.MatchString(line)
	}
	if err := scanner.Err(); err != nil {
		errs = append(errs, err)
	}
	return errs, passed, ok
}

func (f *Facts) record(line string) {
	if !strings.Contains(line, "native-evidence ") {
		return
	}
	for _, m := range marker.FindAllStringSubmatch(line, -1) {
		switch m[1] {
		case "docker_server_version":
			f.Docker = m[2]
		case "buildx_version":
			f.Buildx = m[2]
		case "compose_version":
			f.Compose = m[2]
		}
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
