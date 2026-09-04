// Package gittest builds deterministic git repositories for tests and benchmarks.
//
// Determinism is a hard requirement, not a nicety: the fixture scripts pin
// author/committer identity and dates and null out global+system config, so
// SHAs are byte-identical across runs and machines. Without that every golden
// file churns on every run.
package gittest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixturesDir locates testdata/fixtures relative to this source file, so tests
// work regardless of the package they run from.
func fixturesDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("gittest: cannot locate source file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures")
}

func runScript(tb testing.TB, script, dest string, args ...string) {
	tb.Helper()
	cmd := exec.Command(filepath.Join(fixturesDir(), script), append([]string{dest}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("%s: %v\n%s", script, err, out)
	}
}

// Small returns a fresh repo covering the parser edge cases: paths with
// spaces and non-ASCII bytes, a file with no trailing newline, a merge commit,
// and a working tree that is simultaneously staged, unstaged and untracked.
// Each call gets its own copy, so tests may mutate it freely.
//
// The copy is of a cached build rather than a fresh run of the generator. The
// script is 26 git processes, about 0.9s, and the ui package alone asks for one
// per test: caching turns a 95s package into a few seconds, which is the
// difference between running the tests while working and running them after.
func Small(tb testing.TB) string {
	tb.Helper()
	dest := filepath.Join(tb.TempDir(), "small")
	copyRepo(tb, cached(tb, "mksmall.sh"), dest)
	return dest
}

// Big returns a repo with n commits, for benchmarks.
//
// Benchmarks must treat the result as READ-ONLY: it is the cached build itself,
// not a copy, because 100k commits is too much to copy per call. Anything that
// mutates a repo belongs on Small.
func Big(tb testing.TB, n int) string {
	tb.Helper()
	return cached(tb, "mkbig.sh", fmt.Sprintf("%d", n))
}

// cached builds a fixture once and reuses it across runs, keyed by the
// generator and its arguments.
//
// The cache key includes a hash of the scripts: a harness that silently
// measures a stale fixture is worse than a slow one, because the numbers look
// fine and mean nothing.
//
// The build goes to a scratch directory and is renamed into place, because
// `go test ./...` runs packages in parallel and two of them can ask for the
// same fixture at the same moment. Rename is atomic, so the loser of that race
// finds a finished repo rather than half of one. It may also find the winner's
// — hence the tolerance of an existing destination.
func cached(tb testing.TB, script string, args ...string) string {
	tb.Helper()

	key := strings.TrimSuffix(strings.TrimPrefix(script, "mk"), ".sh")
	for _, a := range args {
		key += "-" + a
	}
	dest := filepath.Join(os.TempDir(), fmt.Sprintf("bubblegit-fixture-%s-%s", key, scriptsHash(tb, script)))
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return dest
	}

	scratch, err := os.MkdirTemp(os.TempDir(), "bubblegit-fixture-building-")
	if err != nil {
		tb.Fatalf("fixture scratch dir: %v", err)
	}
	defer os.RemoveAll(scratch)

	build := filepath.Join(scratch, "repo")
	runScript(tb, script, build, args...)
	if err := os.Rename(build, dest); err != nil && !os.IsExist(err) {
		// A concurrent builder that got there first is not an error; anything
		// else is.
		if _, statErr := os.Stat(filepath.Join(dest, ".git")); statErr != nil {
			tb.Fatalf("publishing fixture %s: %v", dest, err)
		}
	}
	return dest
}

// copyRepo copies a cached fixture so the caller may mutate it.
//
// -p keeps the timestamps. git calls an index entry whose mtime is not
// strictly older than the index itself "racy clean" and re-hashes the file on
// every status; a copy with fresh mtimes is entirely racy, which is the same
// artifact mkbig.sh backdates its worktree to avoid.
func copyRepo(tb testing.TB, src, dest string) {
	tb.Helper()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		tb.Fatalf("fixture copy: %v", err)
	}
	if out, err := exec.Command("cp", "-Rp", src+"/.", dest).CombinedOutput(); err != nil {
		tb.Fatalf("copying fixture %s: %v\n%s", src, err, out)
	}
}

// scriptsHash fingerprints the fixture generator so editing a script
// invalidates every cached repo built by the old one.
func scriptsHash(tb testing.TB, script string) string {
	tb.Helper()
	h := sha256.New()
	for _, name := range []string{"common.sh", script} {
		b, err := os.ReadFile(filepath.Join(fixturesDir(), name))
		if err != nil {
			tb.Fatalf("hashing fixture scripts: %v", err)
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
