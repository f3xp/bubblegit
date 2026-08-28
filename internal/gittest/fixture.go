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
func Small(tb testing.TB) string {
	tb.Helper()
	dest := filepath.Join(tb.TempDir(), "small")
	runScript(tb, "mksmall.sh", dest)
	return dest
}

// Big returns a repo with n commits, for benchmarks.
//
// It is cached across runs in the OS temp dir because building 100k commits
// takes ~11s. The cache key includes a hash of the generator scripts: a
// benchmark harness that silently measures a stale fixture is worse than a
// slow one, because the numbers look fine and mean nothing.
//
// Benchmarks must treat the result as READ-ONLY. Anything that mutates a repo
// belongs on Small.
func Big(tb testing.TB, n int) string {
	tb.Helper()
	dest := filepath.Join(os.TempDir(),
		fmt.Sprintf("bubblegit-fixture-big-%d-%s", n, scriptsHash(tb)))
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return dest
	}
	runScript(tb, "mkbig.sh", dest, fmt.Sprintf("%d", n))
	return dest
}

// scriptsHash fingerprints the fixture generator so editing a script
// invalidates every cached repo built by the old one.
func scriptsHash(tb testing.TB) string {
	tb.Helper()
	h := sha256.New()
	for _, name := range []string{"common.sh", "mkbig.sh"} {
		b, err := os.ReadFile(filepath.Join(fixturesDir(), name))
		if err != nil {
			tb.Fatalf("hashing fixture scripts: %v", err)
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
