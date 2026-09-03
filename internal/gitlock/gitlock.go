// Package gitlock serializes git operations across the concurrently-running
// per-language generation processes that parallel `mage gen:all` now spawns.
//
// Every one of the fourteen client Magefiles (seven proto-clients/*, seven
// spicedb-*) shells out to git internally: a burst of reads at the start of
// Gen() (git diff, git status, git rev-parse) and, on failure, writes that
// roll the working tree back (git checkout, git clean). Run sequentially,
// as they always have been, these never contend. Run concurrently, they
// contend on the repository's single .git/index.lock -- and the write path
// is the dangerous half of that: a lost race there used to leave a dirty
// tree that the root Magefile's commitIfChanged would then sweep into the
// generated commit.
//
// Do gives every one of those call sites -- in all fourteen Magefiles' own
// process, since each runs as a separate `mage -d <dir> gen` subprocess, not
// a goroutine of the root Magefile -- a single, cross-process exclusion
// point. It is implemented with an advisory flock(2) lock on a fixed path
// inside this repository's .git directory, so it is scoped to this checkout
// alone and cannot collide with a developer's git usage in some other clone.
package gitlock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// lockFileName is the fixed path, relative to the repository's git
// directory, of the advisory lock file every Do call contends on. It is
// created on first use and never removed -- flock's exclusion works on the
// inode via an open file descriptor, not on the file's continued existence
// mattering beyond that.
const lockFileName = "spicedb-clients-gen.lock"

// Do runs fn while holding an exclusive advisory lock on this repository, so
// concurrent generations never contend on .git/index.lock.
//
// It blocks until the lock is acquired -- there is no failure mode for
// contention, only for being unable to locate or open the lock file -- and
// it always releases the lock before returning, including when fn panics:
// the unlock and the fd close are both deferred, so they run during panic
// unwinding same as any other return path.
func Do(fn func() error) error {
	path, err := lockPath()
	if err != nil {
		return fmt.Errorf("gitlock: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("gitlock: open lock file %s: %w", path, err)
	}
	defer f.Close()

	// flock(2) locks are associated with an open file description, not with
	// the calling process: a second Do call from another goroutine in this
	// same process opens its own fd here and contends for real, exactly as a
	// separate `mage -d <dir> gen` process would. LOCK_EX with no LOCK_NB
	// blocks the calling goroutine (and only that goroutine -- Go's runtime
	// parks it off a dedicated OS thread for the syscall) until the lock is
	// free.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("gitlock: acquire lock on %s: %w", path, err)
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}()

	return fn()
}

// lockPath returns the fixed lock-file path inside the repository's git
// directory, resolved from the current working directory upward. Mage runs
// each child Magefile with its own subprocess cwd'd into that client's
// directory (`mage -d <dir> gen`), so this cannot be a path fixed at build
// time -- it has to be found fresh, relative to wherever the caller happens
// to be inside the checkout.
func lockPath() (string, error) {
	gitDir, err := findGitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, lockFileName), nil
}

// findGitDir walks upward from the current working directory looking for a
// `.git` entry, and resolves it to the repository's real git directory: a
// plain checkout has `.git` as that directory directly, while a linked
// worktree has it as a file containing "gitdir: <path>", which is resolved
// (including collapsing a `.../worktrees/<name>` suffix down to the shared
// directory every worktree's refs and objects live under) so every caller
// contends on the same lock file regardless of which worktree it started in.
func findGitDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	for {
		candidate := filepath.Join(dir, ".git")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.IsDir() {
				return candidate, nil
			}
			return resolveGitDirFile(candidate)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .git found above %s", dir)
		}
		dir = parent
	}
}

// resolveGitDirFile reads a worktree's `.git` file -- a single line of the
// form "gitdir: <path>" -- and returns the shared repository git directory
// it points at, collapsing a `.../worktrees/<name>` suffix down to its
// parent so a linked worktree resolves to the same lock file as the main
// checkout.
func resolveGitDirFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	line := strings.TrimSpace(string(data))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("%s does not contain a %q line", path, prefix)
	}

	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(path), gitDir)
	}

	sep := string(filepath.Separator)
	if idx := strings.Index(gitDir, sep+"worktrees"+sep); idx >= 0 {
		gitDir = gitDir[:idx]
	}

	return filepath.Clean(gitDir), nil
}
