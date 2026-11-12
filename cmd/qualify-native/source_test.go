package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyReleaseSource(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	git := func(args ...string) string { return testGit(t, args...) }
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("CHANGELOG.md", "before")
	write(".release-please-manifest.json", "before")
	write("other", "before")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "candidate")
	candidate := git("rev-parse", "HEAD")
	write("CHANGELOG.md", "after")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "metadata")
	release := git("rev-parse", "HEAD")
	if err := verifyReleaseSource(candidate, release); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseSource(release, candidate); err == nil {
		t.Fatal("accepted non-ancestor")
	}
	write("other", "after")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "other")
	if err := verifyReleaseSource(candidate, git("rev-parse", "HEAD")); err == nil {
		t.Fatal("accepted other change")
	}
	testReleaseDeletions(t, candidate, release)
}

func testReleaseDeletions(t *testing.T, candidate, release string) {
	git := func(args ...string) string { return testGit(t, args...) }
	git("reset", "--hard", release)
	git("mv", "CHANGELOG.md", "renamed")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "rename")
	if err := verifyReleaseSource(candidate, git("rev-parse", "HEAD")); err == nil {
		t.Fatal("accepted rename")
	}
	git("reset", "--hard", release)
	git("rm", "CHANGELOG.md")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "delete")
	if err := verifyReleaseSource(candidate, git("rev-parse", "HEAD")); err == nil {
		t.Fatal("accepted deletion")
	}
}

func testGit(t *testing.T, args ...string) string {
	t.Helper()
	out, err := output("git", args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
