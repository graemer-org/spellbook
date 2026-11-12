// Package gitx fetches registry repositories with the system git.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Never block a sync on a credential prompt.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Fetch brings the checkout in dir to the given ref of url and returns the
// commit it now points at. An empty ref means the remote's default branch.
// The same shallow fetch is used for the first and for later calls, so a
// branch, a tag and a commit SHA all work as ref.
func Fetch(ctx context.Context, url, ref, dir string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is not installed or not on PATH")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		if _, err := run(ctx, dir, "init", "--quiet"); err != nil {
			return "", err
		}
		if _, err := run(ctx, dir, "remote", "add", "origin", url); err != nil {
			return "", err
		}
	} else if _, err := run(ctx, dir, "remote", "set-url", "origin", url); err != nil {
		return "", err
	}
	want := ref
	if want == "" {
		want = "HEAD"
	}
	if _, err := run(ctx, dir, "fetch", "--quiet", "--depth", "1", "origin", want); err != nil {
		return "", err
	}
	if _, err := run(ctx, dir, "checkout", "--quiet", "--force", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}
	// Drop files a previous commit left behind and that are now untracked.
	if _, err := run(ctx, dir, "clean", "--quiet", "-fdx"); err != nil {
		return "", err
	}
	return run(ctx, dir, "rev-parse", "HEAD")
}

// Diff returns the textual difference between two commits of the checkout
// in dir, limited to subpath when it is not empty. A commit that is no
// longer in the shallow checkout is fetched first.
func Diff(ctx context.Context, dir, from, to, subpath string, stat bool) (string, error) {
	for _, c := range []string{from, to} {
		if _, err := run(ctx, dir, "cat-file", "-e", c+"^{commit}"); err != nil {
			if _, err := run(ctx, dir, "fetch", "--quiet", "--depth", "1", "origin", c); err != nil {
				return "", err
			}
		}
	}
	args := []string{"diff", "--no-color"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, from, to)
	if subpath != "" {
		args = append(args, "--", subpath)
	}
	return run(ctx, dir, args...)
}
