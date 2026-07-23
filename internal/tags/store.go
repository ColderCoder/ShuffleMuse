package tags

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.etcd.io/bbolt"
)

var validTagRe = regexp.MustCompile(`^[a-zA-Z0-9\-_.]+$`)

const (
	maxTagLen   = 50
	filesBucket = "files"
	tagsBucket  = "tags"
)

type TagInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type GraveyardEntry struct {
	Filepath string   `json:"filepath"`
	Name     string   `json:"name"`
	Dir      string   `json:"dir"`
	Tags     []string `json:"tags"`
}

type FileTagRecord struct {
	Filepath string
	Tags     []string
}

var ErrFileOnline = errors.New("tagged file is online")

type Store struct {
	db *bbolt.DB
}

func Open(path string) (*Store, error) {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(filesBucket)); err != nil {
			return fmt.Errorf("create files bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(tagsBucket)); err != nil {
			return fmt.Errorf("create tags bucket: %w", err)
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func validateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag must not be empty")
	}
	if strings.Contains(tag, "/") || strings.Contains(tag, "\\") {
		return fmt.Errorf("tag must not contain / or \\")
	}
	if len(tag) > maxTagLen {
		return fmt.Errorf("tag must be at most %d characters", maxTagLen)
	}
	if !validTagRe.MatchString(tag) {
		return fmt.Errorf("tag contains invalid characters; allowed: alphanumeric, -, _, .")
	}
	return nil
}

func (s *Store) AddTag(filepath, tag string) error {
	if err := validateTag(tag); err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		fb := tx.Bucket([]byte(filesBucket))
		tb := tx.Bucket([]byte(tagsBucket))

		var fileTags []string
		if v := fb.Get([]byte(filepath)); v != nil {
			if err := json.Unmarshal(v, &fileTags); err != nil {
				return fmt.Errorf("unmarshal file tags: %w", err)
			}
		}
		for _, t := range fileTags {
			if t == tag {
				return fmt.Errorf("tag %q already exists for %q", tag, filepath)
			}
		}
		fileTags = append(fileTags, tag)
		data, err := json.Marshal(fileTags)
		if err != nil {
			return fmt.Errorf("marshal file tags: %w", err)
		}
		if err := fb.Put([]byte(filepath), data); err != nil {
			return err
		}

		var tagFiles []string
		if v := tb.Get([]byte(tag)); v != nil {
			if err := json.Unmarshal(v, &tagFiles); err != nil {
				return fmt.Errorf("unmarshal tag files: %w", err)
			}
		}
		tagFiles = append(tagFiles, filepath)
		data, err = json.Marshal(tagFiles)
		if err != nil {
			return fmt.Errorf("marshal tag files: %w", err)
		}
		return tb.Put([]byte(tag), data)
	})
}

func (s *Store) RemoveTag(filepath, tag string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		fb := tx.Bucket([]byte(filesBucket))
		tb := tx.Bucket([]byte(tagsBucket))

		var fileTags []string
		if v := fb.Get([]byte(filepath)); v != nil {
			if err := json.Unmarshal(v, &fileTags); err != nil {
				return fmt.Errorf("unmarshal file tags: %w", err)
			}
		}
		fileTags = removeFromSlice(fileTags, tag)
		if len(fileTags) == 0 {
			if err := fb.Delete([]byte(filepath)); err != nil {
				return err
			}
		} else {
			data, err := json.Marshal(fileTags)
			if err != nil {
				return fmt.Errorf("marshal file tags: %w", err)
			}
			if err := fb.Put([]byte(filepath), data); err != nil {
				return err
			}
		}

		var tagFiles []string
		if v := tb.Get([]byte(tag)); v != nil {
			if err := json.Unmarshal(v, &tagFiles); err != nil {
				return fmt.Errorf("unmarshal tag files: %w", err)
			}
		}
		tagFiles = removeFromSlice(tagFiles, filepath)
		if len(tagFiles) == 0 {
			if err := tb.Delete([]byte(tag)); err != nil {
				return err
			}
		} else {
			data, err := json.Marshal(tagFiles)
			if err != nil {
				return fmt.Errorf("marshal tag files: %w", err)
			}
			if err := tb.Put([]byte(tag), data); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) GetTags(filepath string) ([]string, error) {
	var tags []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(filesBucket))
		v := b.Get([]byte(filepath))
		if v == nil {
			tags = []string{}
			return nil
		}
		return json.Unmarshal(v, &tags)
	})
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}

func (s *Store) GetFilesByTag(tag string) ([]string, error) {
	var files []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(tagsBucket))
		v := b.Get([]byte(tag))
		if v == nil {
			files = []string{}
			return nil
		}
		return json.Unmarshal(v, &files)
	})
	if err != nil {
		return nil, err
	}
	if files == nil {
		files = []string{}
	}
	return files, nil
}

// ForEachFileByTag visits the persisted paths for one tag without first
// materializing the complete JSON array as a []string. The read transaction
// gives the caller one consistent tag snapshot, while callback cancellation
// can stop a large traversal promptly.
func (s *Store) ForEachFileByTag(tag string, visit func(string) error) error {
	return s.db.View(func(tx *bbolt.Tx) error {
		value := tx.Bucket([]byte(tagsBucket)).Get([]byte(tag))
		if value == nil {
			return nil
		}
		return forEachJSONString(value, visit)
	})
}

func forEachJSONString(data []byte, visit func(string) error) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '[' {
		return fmt.Errorf("expected JSON string array")
	}
	for decoder.More() {
		var value string
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if err := visit(value); err != nil {
			return err
		}
	}
	if token, err = decoder.Token(); err != nil {
		return err
	}
	if delimiter, ok = token.(json.Delim); !ok || delimiter != ']' {
		return fmt.Errorf("expected end of JSON string array")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func (s *Store) GetAllTags() ([]TagInfo, error) {
	var result []TagInfo
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(tagsBucket))
		return b.ForEach(func(k, v []byte) error {
			var files []string
			if err := json.Unmarshal(v, &files); err != nil {
				return fmt.Errorf("unmarshal tag %q: %w", string(k), err)
			}
			result = append(result, TagInfo{
				Name:  string(k),
				Count: len(files),
			})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = []TagInfo{}
	}
	return result, nil
}

// GetTaggedFiles returns every persisted file-to-tag association, including
// paths that are currently offline. Results and each record's tags are sorted
// so exports remain stable across runs.
func (s *Store) GetTaggedFiles() ([]FileTagRecord, error) {
	records := make([]FileTagRecord, 0)
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(filesBucket)).ForEach(func(k, v []byte) error {
			var fileTags []string
			if err := json.Unmarshal(v, &fileTags); err != nil {
				return fmt.Errorf("unmarshal file tags for %q: %w", string(k), err)
			}
			sort.Strings(fileTags)
			records = append(records, FileTagRecord{
				Filepath: string(k),
				Tags:     fileTags,
			})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Filepath < records[j].Filepath
	})
	return records, nil
}

// GetOnlineTags counts only paths in a successfully published index snapshot.
func (s *Store) GetOnlineTags(onlinePaths map[string]bool) ([]TagInfo, error) {
	result := make([]TagInfo, 0)
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(tagsBucket)).ForEach(func(k, v []byte) error {
			count := 0
			if err := forEachJSONString(v, func(file string) error {
				if onlinePaths[file] {
					count++
				}
				return nil
			}); err != nil {
				return fmt.Errorf("unmarshal tag %q: %w", string(k), err)
			}
			if count > 0 {
				result = append(result, TagInfo{Name: string(k), Count: count})
			}
			return nil
		})
	})
	return result, err
}

// GetGraveyard returns missing tagged paths in stable filepath order.
func (s *Store) GetGraveyard(onlinePaths map[string]bool, page, limit int) ([]GraveyardEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		return nil, 0, fmt.Errorf("limit must be positive")
	}
	maxInt := int(^uint(0) >> 1)
	start := maxInt
	if page-1 <= maxInt/limit {
		start = (page - 1) * limit
	}
	entries := make([]GraveyardEntry, 0, limit)
	total := 0
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(filesBucket)).ForEach(func(k, v []byte) error {
			path := string(k)
			if onlinePaths[path] {
				return nil
			}
			position := total
			total++
			if position < start || len(entries) >= limit {
				return nil
			}
			var fileTags []string
			if err := json.Unmarshal(v, &fileTags); err != nil {
				return fmt.Errorf("unmarshal file tags for %q: %w", path, err)
			}
			entries = append(entries, GraveyardEntry{
				Filepath: path,
				Name:     strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
				Dir:      filepath.Dir(path),
				Tags:     fileTags,
			})
			return nil
		})
	})
	if err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// DeleteOrphan removes all forward and reverse associations in one bbolt
// transaction. The callback is the API's integration hook for returning 409
// when the supplied/current index says the path is online.
func (s *Store) DeleteOrphan(filepath string, isOnline func(string) bool) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		if isOnline != nil && isOnline(filepath) {
			return ErrFileOnline
		}
		fb := tx.Bucket([]byte(filesBucket))
		if fb.Get([]byte(filepath)) == nil {
			return nil
		}
		return removeFileEntry(fb, tx.Bucket([]byte(tagsBucket)), filepath)
	})
}

func removeFromSlice(s []string, item string) []string {
	for i, v := range s {
		if v == item {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func appendUnique(dst []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, current := range dst {
			if current == value {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, value)
		}
	}
	return dst
}

// MigrateToRelativePaths migrates absolute keys only when the caller provides
// the exact legacy music root. Entries outside that root, ambiguous historical
// paths, and paths absent from the current index remain in the Graveyard.
func (s *Store) MigrateToRelativePaths(knownPaths map[string]bool, legacyRoots ...string) (int, error) {
	if len(legacyRoots) == 0 || strings.TrimSpace(legacyRoots[0]) == "" {
		return 0, nil
	}
	legacyRoot := filepath.Clean(legacyRoots[0])
	migratedCount := 0

	err := s.db.Update(func(tx *bbolt.Tx) error {
		fb := tx.Bucket([]byte(filesBucket))
		tb := tx.Bucket([]byte(tagsBucket))

		var keysToProcess []string
		err := fb.ForEach(func(k, v []byte) error {
			key := string(k)
			if strings.HasPrefix(key, "/") {
				keysToProcess = append(keysToProcess, key)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("iterate files bucket: %w", err)
		}

		for _, oldPath := range keysToProcess {
			newPath := relativePathWithinLegacyRoot(oldPath, legacyRoot, knownPaths)

			if newPath == "" {
				continue
			}

			var fileTags []string
			if v := fb.Get([]byte(oldPath)); v != nil {
				if err := json.Unmarshal(v, &fileTags); err != nil {
					return fmt.Errorf("unmarshal file tags for %q: %w", oldPath, err)
				}
			}

			var existingTags []string
			if v := fb.Get([]byte(newPath)); v != nil {
				if err := json.Unmarshal(v, &existingTags); err != nil {
					return fmt.Errorf("unmarshal existing file tags for %q: %w", newPath, err)
				}
			}
			fileTags = appendUnique(existingTags, fileTags...)
			data, err := json.Marshal(fileTags)
			if err != nil {
				return fmt.Errorf("marshal file tags: %w", err)
			}
			if err := fb.Put([]byte(newPath), data); err != nil {
				return fmt.Errorf("put new file entry: %w", err)
			}

			if err := fb.Delete([]byte(oldPath)); err != nil {
				return fmt.Errorf("delete old file entry: %w", err)
			}

			for _, tag := range fileTags {
				if err := updateTagFileReference(tb, tag, oldPath, newPath); err != nil {
					return fmt.Errorf("update tag reference for %q: %w", tag, err)
				}
			}

			migratedCount++
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	return migratedCount, nil
}

func relativePathWithinLegacyRoot(oldPath, legacyRoot string, knownPaths map[string]bool) string {
	relative, err := filepath.Rel(legacyRoot, filepath.Clean(oldPath))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	if knownPaths[relative] {
		return relative
	}
	return ""
}

func updateTagFileReference(tb *bbolt.Bucket, tag, oldPath, newPath string) error {
	var tagFiles []string
	if v := tb.Get([]byte(tag)); v != nil {
		if err := json.Unmarshal(v, &tagFiles); err != nil {
			return fmt.Errorf("unmarshal tag files: %w", err)
		}
	}

	tagFiles = removeFromSlice(tagFiles, oldPath)
	tagFiles = appendUnique(tagFiles, newPath)

	data, err := json.Marshal(tagFiles)
	if err != nil {
		return fmt.Errorf("marshal tag files: %w", err)
	}
	return tb.Put([]byte(tag), data)
}

func removeFileEntry(fb, tb *bbolt.Bucket, filepath string) error {
	var fileTags []string
	if v := fb.Get([]byte(filepath)); v != nil {
		if err := json.Unmarshal(v, &fileTags); err != nil {
			return fmt.Errorf("unmarshal file tags: %w", err)
		}
	}

	for _, tag := range fileTags {
		var tagFiles []string
		if v := tb.Get([]byte(tag)); v != nil {
			if err := json.Unmarshal(v, &tagFiles); err != nil {
				return fmt.Errorf("unmarshal tag files: %w", err)
			}
		}
		tagFiles = removeFromSlice(tagFiles, filepath)
		if len(tagFiles) == 0 {
			if err := tb.Delete([]byte(tag)); err != nil {
				return err
			}
		} else {
			data, err := json.Marshal(tagFiles)
			if err != nil {
				return fmt.Errorf("marshal tag files: %w", err)
			}
			if err := tb.Put([]byte(tag), data); err != nil {
				return err
			}
		}
	}

	return fb.Delete([]byte(filepath))
}
