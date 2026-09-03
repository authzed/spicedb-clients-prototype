package gitlock

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mkRepo creates a fresh temp directory containing a `.git` directory (as a
// real checkout would) and chdirs the test into it, so findGitDir has
// something real to walk up to without ever touching this repository's own
// .git or its real lock file.
func mkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	t.Chdir(dir)
	return dir
}

// TestDoProvidesMutualExclusion is the core correctness property: many
// goroutines calling Do concurrently must never have more than one of them
// inside fn at once. Each goroutine records how many holders were active
// (including itself) the instant it entered; the max observed across all of
// them must be exactly 1.
func TestDoProvidesMutualExclusion(t *testing.T) {
	mkRepo(t)

	const goroutines = 16
	var active int32
	var maxActive int32
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Do(func() error {
				n := atomic.AddInt32(&active, 1)
				for {
					m := atomic.LoadInt32(&maxActive)
					if n <= m {
						break
					}
					if atomic.CompareAndSwapInt32(&maxActive, m, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return nil
			})
			if err != nil {
				t.Errorf("Do: unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max concurrent holders of the lock = %d, want 1 -- Do is not providing mutual exclusion", got)
	}
}

// TestDoReturnsFnError pins that Do passes fn's own error straight through,
// so a caller (e.g. a rollback that used to discard this error with `_ =`)
// can act on it.
func TestDoReturnsFnError(t *testing.T) {
	mkRepo(t)

	sentinel := errors.New("boom")
	if err := Do(func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("Do() = %v, want %v", err, sentinel)
	}
}

// TestDoReleasesLockAfterError proves the lock is released even when fn
// errors: a second, independent Do call must still be able to acquire it,
// promptly, rather than deadlocking behind a lock nothing ever released.
func TestDoReleasesLockAfterError(t *testing.T) {
	mkRepo(t)

	if err := Do(func() error { return errors.New("boom") }); err == nil {
		t.Fatal("expected the first Do call to return its fn's error")
	}

	done := make(chan error, 1)
	go func() { done <- Do(func() error { return nil }) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Do failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Do never acquired the lock; the error path leaked it")
	}
}

// TestDoReleasesLockAfterPanic proves the lock is released even when fn
// panics -- the design's explicit requirement that release happen "on every
// path including panic". The unlock is deferred, so it must run during
// panic unwinding same as any other deferred cleanup.
func TestDoReleasesLockAfterPanic(t *testing.T) {
	mkRepo(t)

	func() {
		defer func() { _ = recover() }()
		_ = Do(func() error { panic("boom") })
	}()

	done := make(chan error, 1)
	go func() { done <- Do(func() error { return nil }) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Do failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Do never acquired the lock; the panic path leaked it")
	}
}

// TestDoCreatesLockFileInsideGitDir pins where the lock lives: a fixed path
// inside .git, so it is scoped to this repository and cannot collide with a
// developer's git usage in some other checkout.
func TestDoCreatesLockFileInsideGitDir(t *testing.T) {
	dir := mkRepo(t)

	if err := Do(func() error { return nil }); err != nil {
		t.Fatalf("Do: %v", err)
	}

	want := filepath.Join(dir, ".git", lockFileName)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected lock file at %s: %v", want, err)
	}
}

// TestFindGitDirWalksUpward covers the reason lockPath cannot be computed
// once at build time: mage cds each child Magefile's subprocess into that
// client's own directory, so the lock file must be found fresh, relative to
// wherever the caller happens to be inside the checkout.
func TestFindGitDirWalksUpward(t *testing.T) {
	dir := mkRepo(t)
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	t.Chdir(nested)

	got, err := findGitDir()
	if err != nil {
		t.Fatalf("findGitDir: %v", err)
	}
	if want := filepath.Join(dir, ".git"); got != want {
		t.Fatalf("findGitDir() = %s, want %s", got, want)
	}
}

// TestFindGitDirErrorsWhenNoGitAbove ensures a missing .git surfaces as an
// error rather than, say, silently falling back to a lock file relative to
// cwd -- which would defeat the "one lock per repository" property.
func TestFindGitDirErrorsWhenNoGitAbove(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := findGitDir(); err == nil {
		t.Fatal("expected an error when no .git directory exists above cwd, got nil")
	}
}

// TestResolveGitDirFileCollapsesWorktreesSuffix covers a linked worktree's
// .git file, whose "gitdir:" line points at a per-worktree directory nested
// under the shared repository's `worktrees/<name>`. Every caller -- the main
// checkout and any worktree -- must resolve to the same shared directory, or
// they would each lock a different file and stop excluding each other.
func TestResolveGitDirFileCollapsesWorktreesSuffix(t *testing.T) {
	dir := t.TempDir()
	gitFile := filepath.Join(dir, ".git")
	common := filepath.Join(dir, "common-git-dir")
	worktreeGitDir := filepath.Join(common, "worktrees", "my-worktree")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+worktreeGitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	got, err := resolveGitDirFile(gitFile)
	if err != nil {
		t.Fatalf("resolveGitDirFile: %v", err)
	}
	if got != common {
		t.Fatalf("resolveGitDirFile() = %s, want %s", got, common)
	}
}

// TestResolveGitDirFileRelativePath covers a relative "gitdir:" target,
// resolved against the directory containing the .git file itself.
func TestResolveGitDirFileRelativePath(t *testing.T) {
	dir := t.TempDir()
	gitFile := filepath.Join(dir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: ../bare/worktrees/wt\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	got, err := resolveGitDirFile(gitFile)
	if err != nil {
		t.Fatalf("resolveGitDirFile: %v", err)
	}
	if want := filepath.Clean(filepath.Join(dir, "..", "bare")); got != want {
		t.Fatalf("resolveGitDirFile() = %s, want %s", got, want)
	}
}

// TestResolveGitDirFileErrorsOnBadFormat ensures a .git file that is not the
// "gitdir: <path>" format git actually writes surfaces as an error instead
// of, say, silently locking the wrong file.
func TestResolveGitDirFileErrorsOnBadFormat(t *testing.T) {
	dir := t.TempDir()
	gitFile := filepath.Join(dir, ".git")
	if err := os.WriteFile(gitFile, []byte("not a gitdir line\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	if _, err := resolveGitDirFile(gitFile); err == nil {
		t.Fatal("expected an error for a malformed .git file, got nil")
	}
}
