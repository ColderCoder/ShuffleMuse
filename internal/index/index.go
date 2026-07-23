package index

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ErrPathOutsideRoot = errors.New("path resolves outside music directory")

var AudioExtensions = map[string]bool{
	".flac": true,
	".mp3":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".aac":  true,
	".m4a":  true,
	".wma":  true,
}

type FileEntry struct {
	ID       string
	Filepath string
	Name     string
	Dir      string
}

func (e *FileEntry) Absolute(musicDir string) string {
	return filepath.Join(musicDir, e.Filepath)
}

// ResolveWithinRoot resolves a media path, including symlinks, and verifies
// that its final target remains below musicDir.
func ResolveWithinRoot(musicDir, mediaPath string) (string, error) {
	resolver, err := NewRootResolver(musicDir)
	if err != nil {
		return "", err
	}
	return resolver.Resolve(mediaPath)
}

type Index struct {
	Files []FileEntry
	ByID  map[string]*FileEntry
}

func GenerateID(path string) string {
	h := sha256.Sum256([]byte(filepath.Clean(path)))
	return base64.RawURLEncoding.EncodeToString(h[:16])
}

func NormalizePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs)
	}
	return resolved
}

// Scan is the backwards-compatible, non-cancellable scanner entry point.
func Scan(dir string) (*Index, error) {
	return ScanContext(context.Background(), dir)
}

// ScanContext builds a complete in-memory index. It only consumes directory
// entries; it does not open media files or read their metadata. Any traversal
// error aborts the entire scan, so callers can safely publish only nil-error
// results.
func ScanContext(ctx context.Context, dir string) (*Index, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve music root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve music root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat music root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("music root %q is not a directory", root)
	}

	files := make([]FileEntry, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !AudioExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}

		// Ordinary files are already known to be lexically under root by the
		// walk. Only symlinks pay the cost of resolving their real target.
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolve symlink %q: %w", path, err)
			}
			if !withinRoot(root, target) {
				return nil
			}
			targetInfo, err := os.Stat(target)
			if err != nil {
				return fmt.Errorf("stat symlink target %q: %w", path, err)
			}
			if !targetInfo.Mode().IsRegular() {
				return nil
			}
		} else if !entry.Type().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("make %q relative to music root: %w", path, err)
		}
		files = append(files, FileEntry{
			ID:       GenerateID(relPath),
			Filepath: relPath,
			Name:     strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Dir:      filepath.Dir(relPath),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}

	idx := &Index{Files: files, ByID: make(map[string]*FileEntry, len(files))}
	for i := range idx.Files {
		idx.ByID[idx.Files[i].ID] = &idx.Files[i]
	}
	return idx, nil
}

func withinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
