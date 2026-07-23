package tags

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

func testDBPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.db")
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := testDBPath(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenClose(t *testing.T) {
	path := testDBPath(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Verify the DB file was created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected DB file to exist after Open")
	}
}

func TestAddTag(t *testing.T) {
	s := openTestStore(t)

	err := s.AddTag("/music/song.flac", "favorite")
	if err != nil {
		t.Fatalf("AddTag: %v", err)
	}

	tags, err := s.GetTags("/music/song.flac")
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 1 || tags[0] != "favorite" {
		t.Fatalf("expected [favorite], got %v", tags)
	}
}

func TestAddDuplicateTag(t *testing.T) {
	s := openTestStore(t)

	err := s.AddTag("/music/song.flac", "favorite")
	if err != nil {
		t.Fatalf("AddTag first: %v", err)
	}

	err = s.AddTag("/music/song.flac", "favorite")
	if err == nil {
		t.Fatal("expected error for duplicate tag, got nil")
	}
}

func TestRemoveTag(t *testing.T) {
	s := openTestStore(t)

	s.AddTag("/music/song.flac", "favorite")
	err := s.RemoveTag("/music/song.flac", "favorite")
	if err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}

	tags, err := s.GetTags("/music/song.flac")
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected empty tags after remove, got %v", tags)
	}
}

func TestGetFilesByTag(t *testing.T) {
	s := openTestStore(t)

	s.AddTag("/music/rock1.flac", "rock")
	s.AddTag("/music/rock2.flac", "rock")

	files, err := s.GetFilesByTag("rock")
	if err != nil {
		t.Fatalf("GetFilesByTag: %v", err)
	}
	sort.Strings(files)
	expected := []string{"/music/rock1.flac", "/music/rock2.flac"}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	for i, f := range expected {
		if files[i] != f {
			t.Fatalf("expected %s, got %s", f, files[i])
		}
	}
}

func TestForEachFileByTagStreamsAndStopsOnCallbackError(t *testing.T) {
	s := openTestStore(t)
	for _, path := range []string{"one.flac", "two.flac", "three.flac"} {
		if err := s.AddTag(path, "favorite"); err != nil {
			t.Fatal(err)
		}
	}

	stop := errors.New("stop traversal")
	visited := make([]string, 0, 2)
	err := s.ForEachFileByTag("favorite", func(path string) error {
		visited = append(visited, path)
		if len(visited) == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("ForEachFileByTag error = %v, want %v", err, stop)
	}
	if !slices.Equal(visited, []string{"one.flac", "two.flac"}) {
		t.Fatalf("visited = %v", visited)
	}

	called := false
	if err := s.ForEachFileByTag("missing", func(string) error {
		called = true
		return nil
	}); err != nil || called {
		t.Fatalf("missing tag traversal = called:%v err:%v", called, err)
	}
}

func TestGetAllTags(t *testing.T) {
	s := openTestStore(t)

	s.AddTag("/music/song1.flac", "favorite")
	s.AddTag("/music/song2.flac", "rock")
	s.AddTag("/music/song3.flac", "favorite")

	allTags, err := s.GetAllTags()
	if err != nil {
		t.Fatalf("GetAllTags: %v", err)
	}

	tagMap := make(map[string]int)
	for _, ti := range allTags {
		tagMap[ti.Name] = ti.Count
	}

	if tagMap["favorite"] != 2 {
		t.Fatalf("expected favorite count 2, got %d", tagMap["favorite"])
	}
	if tagMap["rock"] != 1 {
		t.Fatalf("expected rock count 1, got %d", tagMap["rock"])
	}
}

func TestGetTaggedFilesReturnsStableRecords(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddTag("z/track.flac", "rock"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTag("a/track.flac", "warm"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTag("a/track.flac", "favorite"); err != nil {
		t.Fatal(err)
	}

	records, err := s.GetTaggedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %v, want 2", records)
	}
	if records[0].Filepath != "a/track.flac" || !slices.Equal(records[0].Tags, []string{"favorite", "warm"}) {
		t.Fatalf("first record = %v, want sorted path and tags", records[0])
	}
	if records[1].Filepath != "z/track.flac" || !slices.Equal(records[1].Tags, []string{"rock"}) {
		t.Fatalf("second record = %v", records[1])
	}
}

func TestTagValidation(t *testing.T) {
	s := openTestStore(t)

	// Empty tag should error
	err := s.AddTag("/music/song.flac", "")
	if err == nil {
		t.Fatal("expected error for empty tag, got nil")
	}

	// Tag with "/" should error
	err = s.AddTag("/music/song.flac", "rock/pop")
	if err == nil {
		t.Fatal("expected error for tag with /, got nil")
	}

	// Tag with "\" should error
	err = s.AddTag("/music/song.flac", `rock\pop`)
	if err == nil {
		t.Fatal("expected error for tag with backslash, got nil")
	}

	// Valid tag with hyphen should succeed
	err = s.AddTag("/music/song.flac", "my-tag")
	if err != nil {
		t.Fatalf("expected my-tag to be valid, got error: %v", err)
	}
}

func TestDualBucketConsistency(t *testing.T) {
	s := openTestStore(t)

	// Add tag — both buckets should be updated
	s.AddTag("/music/song.flac", "favorite")

	// Verify files bucket
	tags, _ := s.GetTags("/music/song.flac")
	if len(tags) != 1 || tags[0] != "favorite" {
		t.Fatalf("files bucket: expected [favorite], got %v", tags)
	}

	// Verify tags bucket
	files, _ := s.GetFilesByTag("favorite")
	if len(files) != 1 || files[0] != "/music/song.flac" {
		t.Fatalf("tags bucket: expected [/music/song.flac], got %v", files)
	}

	// Remove tag — both buckets should be cleaned
	s.RemoveTag("/music/song.flac", "favorite")

	tags, _ = s.GetTags("/music/song.flac")
	if len(tags) != 0 {
		t.Fatalf("files bucket: expected empty after remove, got %v", tags)
	}

	files, _ = s.GetFilesByTag("favorite")
	if len(files) != 0 {
		t.Fatalf("tags bucket: expected empty after remove, got %v", files)
	}
}

func TestMigrateToRelativePaths(t *testing.T) {
	s := openTestStore(t)

	// Insert tags with absolute path key
	s.AddTag("/music/Artist/track.flac", "favorite")
	s.AddTag("/music/Artist/track.flac", "rock")

	// Migrate to relative paths
	knownPaths := map[string]bool{"Artist/track.flac": true}
	count, err := s.MigrateToRelativePaths(knownPaths, "/music")
	if err != nil {
		t.Fatalf("MigrateToRelativePaths: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 migration, got %d", count)
	}

	// Verify old absolute key is gone
	tags, _ := s.GetTags("/music/Artist/track.flac")
	if len(tags) != 0 {
		t.Fatalf("expected no tags for old absolute path, got %v", tags)
	}

	// Verify new relative key has the tags
	tags, err = s.GetTags("Artist/track.flac")
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags for relative path, got %v", tags)
	}

	// Verify tags bucket points to relative path
	files, _ := s.GetFilesByTag("favorite")
	if len(files) != 1 || files[0] != "Artist/track.flac" {
		t.Fatalf("tags bucket: expected [Artist/track.flac], got %v", files)
	}
}

func TestMigrateToRelativePathsWithDifferentPrefix(t *testing.T) {
	s := openTestStore(t)

	// Insert tags with different absolute path prefix
	s.AddTag("/tmp/test-music/Artist/track.flac", "favorite")

	// Migrate only because this exact legacy root was configured.
	knownPaths := map[string]bool{"Artist/track.flac": true}
	count, err := s.MigrateToRelativePaths(knownPaths, "/tmp/test-music")
	if err != nil {
		t.Fatalf("MigrateToRelativePaths: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 migration, got %d", count)
	}

	// Verify new relative key has the tag
	tags, _ := s.GetTags("Artist/track.flac")
	if len(tags) != 1 || tags[0] != "favorite" {
		t.Fatalf("expected [favorite] for relative path, got %v", tags)
	}
}

func TestMigrateToRelativePathsOnlyMatchesComponentBoundaries(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddTag("/old/prefixArtist/track.flac", "favorite"); err != nil {
		t.Fatal(err)
	}
	count, err := s.MigrateToRelativePaths(map[string]bool{"Artist/track.flac": true}, "/old")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("mid-component suffix was migrated: count=%d", count)
	}
	tags, err := s.GetTags("/old/prefixArtist/track.flac")
	if err != nil || len(tags) != 1 || tags[0] != "favorite" {
		t.Fatalf("unmatched absolute path was not retained: tags=%v err=%v", tags, err)
	}
}

func TestMigrateToRelativePathsIdempotent(t *testing.T) {
	s := openTestStore(t)

	// Insert with absolute path
	s.AddTag("/music/Artist/track.flac", "favorite")

	knownPaths := map[string]bool{"Artist/track.flac": true}

	// First migration
	count1, err := s.MigrateToRelativePaths(knownPaths, "/music")
	if err != nil {
		t.Fatalf("first MigrateToRelativePaths: %v", err)
	}
	if count1 != 1 {
		t.Fatalf("expected 1 migration on first run, got %d", count1)
	}

	// Second migration should be no-op
	count2, err := s.MigrateToRelativePaths(knownPaths, "/music")
	if err != nil {
		t.Fatalf("second MigrateToRelativePaths: %v", err)
	}
	if count2 != 0 {
		t.Fatalf("expected 0 migrations on second run, got %d", count2)
	}
}

func TestMigrateToRelativePathsRetainsUnmatchedPaths(t *testing.T) {
	s := openTestStore(t)

	// Insert tags for file that doesn't exist in knownPaths
	s.AddTag("/music/OldArtist/oldtrack.flac", "favorite")

	// Migrate with empty knownPaths
	knownPaths := map[string]bool{}
	count, err := s.MigrateToRelativePaths(knownPaths, "/music")
	if err != nil {
		t.Fatalf("MigrateToRelativePaths: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 migrations (orphan cleanup is not a migration), got %d", count)
	}

	// Unmatched records remain intact for the Graveyard.
	tags, _ := s.GetTags("/music/OldArtist/oldtrack.flac")
	if len(tags) != 1 || tags[0] != "favorite" {
		t.Fatalf("expected unmatched file tags to be retained, got %v", tags)
	}

	files, _ := s.GetFilesByTag("favorite")
	if len(files) != 1 || files[0] != "/music/OldArtist/oldtrack.flac" {
		t.Fatalf("expected reverse association to be retained, got %v", files)
	}
}

func TestMigrateToRelativePathsRequiresExplicitExactRoot(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddTag("/archive/music/Artist/track.flac", "favorite"); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"Artist/track.flac": true}
	for _, roots := range [][]string{nil, {"/music"}} {
		count, err := s.MigrateToRelativePaths(known, roots...)
		if err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("ambiguous suffix migrated with roots %v", roots)
		}
	}
	tags, _ := s.GetTags("/archive/music/Artist/track.flac")
	if len(tags) != 1 {
		t.Fatal("ambiguous path was not retained in Graveyard")
	}
}

func TestOnlineTagsAndGraveyardRestoration(t *testing.T) {
	s := openTestStore(t)
	s.AddTag("Artist/live.flac", "favorite")
	s.AddTag("Missing/gone.mp3", "favorite")
	s.AddTag("Missing/gone.mp3", "lost")

	online := map[string]bool{"Artist/live.flac": true}
	tagInfo, err := s.GetOnlineTags(online)
	if err != nil || len(tagInfo) != 1 || tagInfo[0].Name != "favorite" || tagInfo[0].Count != 1 {
		t.Fatalf("unexpected online tag counts: %v, err=%v", tagInfo, err)
	}
	entries, total, err := s.GetGraveyard(online, 1, 10)
	if err != nil || total != 1 || len(entries) != 1 {
		t.Fatalf("unexpected graveyard: %v total=%d err=%v", entries, total, err)
	}
	if entries[0].Filepath != "Missing/gone.mp3" || entries[0].Name != "gone" || entries[0].Dir != "Missing" || len(entries[0].Tags) != 2 {
		t.Fatalf("unexpected graveyard entry: %+v", entries[0])
	}

	// Restoration is purely snapshot-driven and does not rewrite the database.
	online["Missing/gone.mp3"] = true
	entries, total, err = s.GetGraveyard(online, 1, 10)
	if err != nil || total != 0 || len(entries) != 0 {
		t.Fatalf("restored path remained in graveyard: %v total=%d err=%v", entries, total, err)
	}
	tagInfo, _ = s.GetOnlineTags(online)
	counts := map[string]int{}
	for _, item := range tagInfo {
		counts[item.Name] = item.Count
	}
	if counts["favorite"] != 2 || counts["lost"] != 1 {
		t.Fatalf("restored tags not counted: %v", counts)
	}
}

func TestGraveyardPaginationAndDeleteOrphan(t *testing.T) {
	s := openTestStore(t)
	s.AddTag("c.flac", "x")
	s.AddTag("a.flac", "x")
	s.AddTag("b.flac", "y")

	entries, total, err := s.GetGraveyard(map[string]bool{}, 2, 2)
	if err != nil || total != 3 || len(entries) != 1 || entries[0].Filepath != "c.flac" {
		t.Fatalf("unexpected page: %v total=%d err=%v", entries, total, err)
	}
	if err := s.DeleteOrphan("a.flac", func(string) bool { return true }); !errors.Is(err, ErrFileOnline) {
		t.Fatalf("expected ErrFileOnline, got %v", err)
	}
	if tags, _ := s.GetTags("a.flac"); len(tags) != 1 {
		t.Fatalf("online conflict mutated forward association: %v", tags)
	}
	if err := s.DeleteOrphan("a.flac", func(string) bool { return false }); err != nil {
		t.Fatalf("DeleteOrphan: %v", err)
	}
	if tags, _ := s.GetTags("a.flac"); len(tags) != 0 {
		t.Fatalf("forward association remains: %v", tags)
	}
	if files, _ := s.GetFilesByTag("x"); len(files) != 1 || files[0] != "c.flac" {
		t.Fatalf("reverse association not cleaned transactionally: %v", files)
	}
}
