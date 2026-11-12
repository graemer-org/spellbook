package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func hash(t *testing.T, dir string) string {
	t.Helper()
	h, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCopyHasTheHashOfItsSource(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "SKILL.md"), "body", 0o644)
	write(t, filepath.Join(src, "scripts", "run.sh"), "#!/bin/sh", 0o755)
	write(t, filepath.Join(src, ".git", "HEAD"), "ref", 0o644)
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := ReplaceDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if hash(t, src) != hash(t, dst) {
		t.Error("copy and source have different hashes")
	}
	for _, skipped := range []string{".git", "link"} {
		if _, err := os.Lstat(filepath.Join(dst, skipped)); err == nil {
			t.Errorf("%s should not be copied", skipped)
		}
	}
	info, err := os.Stat(filepath.Join(dst, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("executable bit was lost")
	}
}

func TestHashChangesWithContentNameAndMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	write(t, file, "one", 0o644)
	base := hash(t, dir)

	write(t, file, "two", 0o644)
	if hash(t, dir) == base {
		t.Error("hash ignores content")
	}
	write(t, file, "one", 0o644)
	if hash(t, dir) != base {
		t.Error("hash is not stable")
	}
	if err := os.Chmod(file, 0o755); err != nil {
		t.Fatal(err)
	}
	if hash(t, dir) == base {
		t.Error("hash ignores the executable bit")
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if hash(t, dir) == base {
		t.Error("hash ignores file names")
	}
}

func TestReplaceDirRemovesFilesThatAreGoneFromTheSource(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "keep.md"), "new", 0o644)
	dst := filepath.Join(t.TempDir(), "skill")
	write(t, filepath.Join(dst, "keep.md"), "old", 0o644)
	write(t, filepath.Join(dst, "stale.md"), "old", 0o644)

	if err := ReplaceDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.md")); err == nil {
		t.Error("stale file survived the replace")
	}
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temporary directories were left behind: %v", entries)
	}
}
