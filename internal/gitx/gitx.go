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

// Version returns the version string of the installed git.
func Version(ctx context.Context) (string, error) {
	out, err := run(ctx, "", "--version")
	return strings.TrimPrefix(out, "git version "), err
}

// Reachable checks that the repository at url can be read.
func Reachable(ctx context.Context, url string) error {
	_, err := run(ctx, "", "ls-remote", "--quiet", url, "HEAD")
	return err
}

// RemoteHead returns the commit a ref of the repository at url points at,
// without fetching anything. An empty ref means the default branch.
func RemoteHead(ctx context.Context, url, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	// For an annotated tag the second pattern yields the commit behind it.
	out, err := run(ctx, "", "ls-remote", "--quiet", url, ref, ref+"^{}")
	if err != nil {
		return "", err
	}
	commit := ""
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if commit == "" || strings.HasSuffix(name, "^{}") {
			commit = sha
		}
	}
	return commit, nil
}

// Uncommitted returns how many of the given paths in the repository at dir
// have changes that are not committed. It returns -1 when dir is not
// inside a git repository.
func Uncommitted(ctx context.Context, dir string, paths []string) int {
	if _, err := run(ctx, dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return -1
	}
	args := append([]string{"status", "--porcelain", "--untracked-files=all", "--"}, paths...)
	out, err := run(ctx, dir, args...)
	if err != nil || out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

// File is a file to add to a repository.
type File struct {
	// Path is slash-separated and relative to the repository root.
	Path string
	Data []byte
	Exec bool
}

// Propose clones the repository at url, puts the files on a new branch
// based on base, commits them and pushes the branch. An empty base means
// the default branch. edit, when not nil, may change the checkout before
// the commit. The paths in remove are deleted before the files are written,
// for a directory that is replaced as a whole. Nothing is pushed when there
// is nothing to commit.
func Propose(ctx context.Context, url, base, branch, message string, remove []string, files []File, edit func(dir string) error) error {
	dir, err := os.MkdirTemp("", "spellbook-push-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	args := []string{"clone", "--quiet", "--depth", "1"}
	if base != "" {
		args = append(args, "--branch", base)
	}
	if _, err := run(ctx, "", append(args, url, dir)...); err != nil {
		if base != "" {
			return fmt.Errorf("%w (the registry's ref %q must be a branch or tag to push from)", err, base)
		}
		return err
	}
	if _, err := run(ctx, dir, "checkout", "--quiet", "-b", branch); err != nil {
		return err
	}
	if err := Remove(dir, remove); err != nil {
		return err
	}
	if err := WriteFiles(dir, files); err != nil {
		return err
	}
	if edit != nil {
		if err := edit(dir); err != nil {
			return err
		}
	}
	if _, err := run(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	if _, err := run(ctx, dir, "diff", "--cached", "--quiet"); err == nil {
		return fmt.Errorf("the registry already contains exactly this; nothing to push")
	}
	if _, err := run(ctx, dir, "commit", "--quiet", "-m", message); err != nil {
		return err
	}
	if _, err := run(ctx, dir, "push", "--quiet", "origin", branch); err != nil {
		return fmt.Errorf("%w (if the branch already exists there, choose another with --branch)", err)
	}
	return nil
}

// Remove deletes the slash-separated paths below dir.
func Remove(dir string, paths []string) error {
	for _, p := range paths {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return fmt.Errorf("refusing to remove %q outside the repository", p)
		}
		if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			return err
		}
	}
	return nil
}

// WriteFiles writes the files below dir, creating directories as needed.
func WriteFiles(dir string, files []File) error {
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.Exec {
			mode = 0o755
		}
		if err := os.WriteFile(path, f.Data, mode); err != nil {
			return err
		}
	}
	return nil
}
