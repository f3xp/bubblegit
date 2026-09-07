package git_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// bigRepoCommits is the benchmark fixture size. The point of this harness is
// to answer "when does spawn-per-call stop being fast enough?" with a
// measurement instead of a guess.
const bigRepoCommits = 100000

// workBudget caps the git work a single read may do, measured ABOVE the
// process-spawn floor.
//
// It is deliberately not an absolute wall-clock budget. Spawn cost is a
// property of the machine, not of our code: on a clean box fork/exec is ~1ms,
// but on a laptop with EDR hooks (CrowdStrike, ManageEngine et al filtering
// every execve) it is ~14ms, which alone exceeds a 60fps frame. Measuring
// above the floor keeps this guard portable and keeps it pointed at the thing
// we can actually fix.
const workBudget = 8 * time.Millisecond

// ops are the read paths on the hot loop.
//
// Each one calls the exported function the UI calls, rather than repeating its
// argument list here. A table of raw args drifts silently: the day the git
// layer changes a flag, this harness keeps passing while measuring a command
// nothing runs any more, and a stale guard is worse than none because the
// numbers still look fine.
//
// Pagination is cursor-based (start the walk at a SHA), never offset-based.
// `--skip=N` is O(N) — git walks and discards every skipped commit — so page
// 500 of the log costs 100x page 1. See TestPaginationStrategy.
var ops = []struct {
	name string
	// run performs the read and reports how much came back, so the budget test
	// can tell a working read from one that quietly returns nothing.
	run        func(context.Context, *git.Runner) (int, error)
	mayBeEmpty bool // a clean repo legitimately produces no output
}{
	{
		name:       "Status",
		mayBeEmpty: true,
		run: func(ctx context.Context, r *git.Runner) (int, error) {
			f, err := git.Status(ctx, r)
			return len(f), err
		},
	},
	{
		name: "LogFirstPage",
		run: func(ctx context.Context, r *git.Runner) (int, error) {
			c, err := git.Log(ctx, r, nil, git.LogPageSize)
			return len(c), err
		},
	},
	{
		// The seed of an all-refs log: one rev-list over every ref, so the
		// walk can resume from tips the first page never reached.
		name: "Tips",
		run: func(ctx context.Context, r *git.Runner) (int, error) {
			t, err := git.Tips(ctx, r)
			return len(t), err
		},
	},
	{
		name: "CommitDetail",
		run: func(ctx context.Context, r *git.Runner) (int, error) {
			d, err := git.Show(ctx, r, "HEAD")
			return len(d.Files), err
		},
	},
	{
		// The branch pane's read. %(upstream:track) makes git compute
		// ahead/behind inside this one call; the obvious alternative costs a
		// `rev-list --count` process per branch, which is what this budget is
		// here to catch if anyone reaches for it.
		name: "Branches",
		run: func(ctx context.Context, r *git.Runner) (int, error) {
			b, err := git.Branches(ctx, r)
			return len(b), err
		},
	},
}

// bestOf returns the fastest of n runs after a warmup.
//
// The warmup is not optional: the first status against a freshly built repo
// pays a cold index refresh and a cold page cache, and costs ~10x the steady
// state. Timing that measures the fixture builder, not our git usage.
func bestOf(n int, f func()) time.Duration {
	f() // warmup, discarded
	b := time.Duration(1<<63 - 1)
	for i := 0; i < n; i++ {
		start := time.Now()
		f()
		if d := time.Since(start); d < b {
			b = d
		}
	}
	return b
}

// spawnFloor is the cost of creating any process at all on this machine,
// measured with a binary that does nothing. Everything else is reported
// relative to it.
func spawnFloor() time.Duration {
	return bestOf(10, func() { _ = exec.Command("/usr/bin/true").Run() })
}

func benchRepo(tb testing.TB) *git.Runner {
	tb.Helper()
	if testing.Short() {
		tb.Skip("skipping big-fixture benchmark in -short mode")
	}
	return git.New(gittest.Big(tb, bigRepoCommits))
}

func BenchmarkOps(b *testing.B) {
	r := benchRepo(b)
	ctx := context.Background()
	for _, op := range ops {
		b.Run(op.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := op.run(ctx, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestWorkBudget fails when a hot read starts doing too much git work.
// Timing is the minimum of several runs, so scheduler noise and a cold page
// cache cannot turn it into a flake — only a genuine slowdown trips it.
func TestWorkBudget(t *testing.T) {
	r := benchRepo(t)
	ctx := context.Background()

	t.Logf("process spawn floor at the start of this run: %v (all figures below are "+
		"reported above a floor re-measured per op)", spawnFloor().Round(time.Microsecond))

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			// The floor is re-measured beside each op rather than once up
			// front. `go test ./...` builds and runs other packages in
			// parallel, so a floor sampled while the machine was idle and an
			// op timed while it was loaded do not subtract: the difference is
			// the load, not the git work, and it trips whichever op happened
			// to run during the busy stretch.
			floor := spawnFloor()
			total := bestOf(9, func() {
				n, err := op.run(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 && !op.mayBeEmpty {
					t.Fatalf("%s read returned nothing", op.name)
				}
			})
			work := total - floor
			if work < 0 {
				work = 0
			}
			t.Logf("%s: %v wall, %v work (budget %v)",
				op.name, total.Round(time.Microsecond), work.Round(time.Microsecond), workBudget)
			if work > workBudget {
				t.Errorf("%s does %v of git work, over the %v budget — this read needs "+
					"bounding, caching, or a long-lived git process", op.name, work, workBudget)
			}
		})
	}
}

// TestPaginationStrategy pins the reason the log pane must page by cursor.
//
// `--skip=N` makes git walk and throw away N commits, so cost grows with how
// far the user has scrolled. Passing the last SHA of the previous page instead
// makes every page cost the same. This is a property of git, not of the
// machine, so the assertion is a ratio rather than a duration.
func TestPaginationStrategy(t *testing.T) {
	r := benchRepo(t)
	ctx := context.Background()

	const deep = bigRepoCommits / 2

	out, err := r.Run(ctx, "log", "-n", "1", "--skip="+itoa(deep), "--format=%H")
	if err != nil {
		t.Fatal(err)
	}
	cursor := string(out[:40])

	offset := bestOf(3, func() {
		r.Run(ctx, "log", "-n", itoa(git.LogPageSize), "--skip="+itoa(deep), "--format=%H")
	})
	byCursor := bestOf(3, func() {
		r.Run(ctx, "log", "-n", itoa(git.LogPageSize), "--format=%H", cursor)
	})

	t.Logf("page at offset %d: --skip=%v vs cursor=%v",
		deep, offset.Round(time.Microsecond), byCursor.Round(time.Microsecond))

	if byCursor >= offset {
		t.Errorf("cursor paging (%v) is not faster than offset paging (%v); "+
			"if this ever holds, revisit the log pane design", byCursor, offset)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
