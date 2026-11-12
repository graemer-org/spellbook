// Package fsutil hashes and copies resource directories.
//
// Hashing and copying use the same file selection, so a fresh copy always
// has the hash of its source: regular files only, without symlinks and
// without any .git directory.
package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type file struct {
	rel  string
	exec bool
}

func listFiles(root string) ([]file, error) {
	var files []file
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, file{rel: filepath.ToSlash(rel), exec: info.Mode()&0o111 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

// HashDir returns a content hash over the files in root: their relative
// paths, executable bits and contents.
func HashDir(root string) (string, error) {
	files, err := listFiles(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%t\x00", f.rel, f.exec)
		in, err := os.Open(filepath.Join(root, filepath.FromSlash(f.rel)))
		if err != nil {
			return "", err
		}
		n, err := io.Copy(h, in)
		in.Close()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "\x00%d\x00", n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyDir copies the files of src into dst, which must not exist yet.
func CopyDir(src, dst string) error {
	files, err := listFiles(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		from := filepath.Join(src, filepath.FromSlash(f.rel))
		to := filepath.Join(dst, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.exec {
			mode = 0o755
		}
		if err := copyFile(from, to, mode); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ReplaceDir puts a copy of src at dst. An existing dst is swapped out only
// after the new copy is complete, so a failed copy leaves dst untouched.
func ReplaceDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".spellbook-tmp"
	old := dst + ".spellbook-old"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := CopyDir(src, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := os.RemoveAll(old); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		if err := os.Rename(dst, old); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		// Put the previous version back so dst is never left missing.
		if _, statErr := os.Lstat(old); statErr == nil {
			os.Rename(old, dst)
		}
		os.RemoveAll(tmp)
		return err
	}
	return os.RemoveAll(old)
}
