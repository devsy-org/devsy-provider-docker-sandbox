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

func TestVerifyReleaseSourceRejectsTreeAndHistoryChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	git := func(args ...string) string { return testGit(t, args...) }
	git("init", "-q")
	writeSourceFile(t, "CHANGELOG.md", "original")
	writeSourceFile(t, "other", "target")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "candidate")
	candidate := git("rev-parse", "HEAD")
	if err := verifyReleaseSource(candidate, candidate); err != nil {
		t.Fatalf("candidate equals release: %v", err)
	}
	cases := []struct {
		name   string
		change func()
	}{
		{"added file", func() { writeSourceFile(t, "new", "new"); git("add", "new") }},
		{"mode change", func() { git("update-index", "--chmod=+x", "CHANGELOG.md") }},
		{"symlink swap", func() {
			blob := git("hash-object", "-w", "other")
			git("update-index", "--cacheinfo", "120000", blob, "CHANGELOG.md")
		}},
		{
			"type change",
			func() { git("update-index", "--cacheinfo", "160000", candidate, "CHANGELOG.md") },
		},
		{"empty diff", func() {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			git("reset", "--hard", candidate)
			tc.change()
			git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", tc.name)
			if err := verifyReleaseSource(candidate, git("rev-parse", "HEAD")); err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
	git("reset", "--hard", candidate)
	writeSourceFile(t, "CHANGELOG.md", "updated")
	git("add", "CHANGELOG.md")
	tree := git("write-tree")
	side := git("commit-tree", tree, "-p", candidate, "-m", "side")
	merge := git("commit-tree", tree, "-p", candidate, "-p", side, "-m", "merge")
	if err := verifyReleaseSource(candidate, merge); err == nil {
		t.Fatal("accepted merge commit")
	}
}

func writeSourceFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
