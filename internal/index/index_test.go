package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanIndexesAudioWithStableRelativeIDs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Artist", "Album")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"track.flac", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	first, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 || len(second.Files) != 1 {
		t.Fatalf("expected one audio file, got %d and %d", len(first.Files), len(second.Files))
	}
	want := filepath.Join("Artist", "Album", "track.flac")
	if got := first.Files[0].Filepath; got != want {
		t.Fatalf("filepath = %q, want %q", got, want)
	}
	if first.Files[0].ID != second.Files[0].ID || first.Files[0].ID != GenerateID(want) {
		t.Fatal("relative path did not produce a stable ID")
	}
	if first.ByID[first.Files[0].ID] != &first.Files[0] {
		t.Fatal("ByID does not point into Files snapshot")
	}
}

func TestScanEmptyDirectoryIsValid(t *testing.T) {
	idx, err := Scan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Files) != 0 || len(idx.ByID) != 0 {
		t.Fatalf("expected empty snapshot, got %+v", idx)
	}
}

func TestScanSkipsHiddenFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".private"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".hidden.flac", filepath.Join(".private", "track.flac"), "visible.mp3"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Files) != 1 || idx.Files[0].Filepath != "visible.mp3" {
		t.Fatalf("unexpected files: %+v", idx.Files)
	}
}

func TestScanSymlinkRootAndTargets(t *testing.T) {
	realRoot := t.TempDir()
	rootLink := filepath.Join(t.TempDir(), "music")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	inside := filepath.Join(realRoot, "inside.flac")
	outside := filepath.Join(t.TempDir(), "outside.flac")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(inside, filepath.Join(realRoot, "alias.flac")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(realRoot, "escape.flac")); err != nil {
		t.Fatal(err)
	}

	idx, err := Scan(rootLink)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, entry := range idx.Files {
		paths[entry.Filepath] = true
	}
	if len(paths) != 2 || !paths["inside.flac"] || !paths["alias.flac"] {
		t.Fatalf("unexpected symlink scan result: %v", paths)
	}
	if _, err := ResolveWithinRoot(rootLink, "../outside.flac"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("ResolveWithinRoot error = %v", err)
	}
}

func TestScanBrokenSymlinkFailsWholeScan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.flac"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing.flac"), filepath.Join(root, "broken.flac")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	idx, err := Scan(root)
	if err == nil || idx != nil {
		t.Fatalf("expected complete scan failure, idx=%v err=%v", idx, err)
	}
}

func TestScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idx, err := ScanContext(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) || idx != nil {
		t.Fatalf("expected context cancellation, idx=%v err=%v", idx, err)
	}
}

func TestScanRejectsMissingAndNonDirectoryRoots(t *testing.T) {
	if _, err := Scan(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root succeeded")
	}
	file := filepath.Join(t.TempDir(), "music")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(file); err == nil {
		t.Fatal("file root succeeded")
	}
}

func TestFileEntryAbsolute(t *testing.T) {
	entry := FileEntry{Filepath: filepath.Join("Artist", "track.flac")}
	if got, want := entry.Absolute("/music"), filepath.Join("/music", entry.Filepath); got != want {
		t.Fatalf("Absolute = %q, want %q", got, want)
	}
}
