package playqueue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
)

type staticTags map[string][]string

func (s staticTags) GetFilesByTag(tag string) ([]string, error) {
	return append([]string(nil), s[tag]...), nil
}

type blockReader struct {
	mu    sync.Mutex
	next  byte
	calls int
}

type cancelAfterChecks struct {
	remaining int
}

func (c *cancelAfterChecks) Deadline() (time.Time, bool)   { return time.Time{}, false }
func (c *cancelAfterChecks) Done() <-chan struct{}         { return nil }
func (c *cancelAfterChecks) Value(interface{}) interface{} { return nil }
func (c *cancelAfterChecks) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func (r *blockReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	for i := range p {
		p[i] = r.next
		r.next++
	}
	return len(p), nil
}

func testIndex(count int) *index.Index {
	idx := &index.Index{Files: make([]index.FileEntry, count), ByID: make(map[string]*index.FileEntry, count)}
	for i := range idx.Files {
		path := fmt.Sprintf("album/track-%06d.flac", i)
		idx.Files[i] = index.FileEntry{ID: index.GenerateID(path), Filepath: path, Name: fmt.Sprintf("track-%06d", i), Dir: "album"}
	}
	for i := range idx.Files {
		idx.ByID[idx.Files[i].ID] = &idx.Files[i]
	}
	return idx
}

func randomBytes(blocks int) *bytes.Reader {
	data := make([]byte, blocks*64)
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	return bytes.NewReader(data)
}

func TestShuffleDeterministicAndComplete(t *testing.T) {
	order := make([]uint32, 1000)
	for i := range order {
		order[i] = uint32(i)
	}
	copyOrder := append([]uint32(nil), order...)
	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i)
	}
	shuffle(order, seed)
	shuffle(copyOrder, seed)
	if !equalUint32(order, copyOrder) {
		t.Fatal("same ChaCha8 seed produced different Fisher-Yates permutations")
	}
	seen := make([]bool, len(order))
	for _, value := range order {
		if int(value) >= len(seen) || seen[value] {
			t.Fatalf("invalid or duplicate value %d", value)
		}
		seen[value] = true
	}
}

func TestCreateUsesIndependentSeedAndTokenAndPagesStableOrder(t *testing.T) {
	random := &blockReader{}
	m := NewManager(Config{}, nil, Options{Random: random})
	idx := testIndex(1001)
	created, err := m.Create(context.Background(), idx, 7, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if random.calls != 2 {
		t.Fatalf("entropy reads = %d, want one seed plus one token", random.calls)
	}
	secondCreated, err := m.Create(context.Background(), idx, 7, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if random.calls != 4 || secondCreated.Queue.ID == created.Queue.ID {
		t.Fatalf("second creation did not request an independent seed and token: calls=%d", random.calls)
	}
	if len(secondCreated.Items) == len(created.Items) {
		same := true
		for i := range created.Items {
			if created.Items[i].ID != secondCreated.Items[i].ID {
				same = false
				break
			}
		}
		if same {
			t.Fatal("two fixed, distinct entropy blocks produced the same first page")
		}
	}
	if created.Queue.Total != 1001 || len(created.Items) != PageSize || created.LibraryGeneration != 7 {
		t.Fatalf("unexpected first page: %+v", created)
	}
	all := append([]Item(nil), created.Items...)
	for page := 2; page <= 6; page++ {
		result, err := m.Page(created.Queue.ID, page, idx, 7)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, result.Items...)
	}
	if len(all) != 1001 {
		t.Fatalf("concatenated pages = %d", len(all))
	}
	seen := make(map[string]bool, len(all))
	for position, item := range all {
		if item.QueueIndex != position || seen[item.ID] {
			t.Fatalf("position %d contains duplicate or wrong queue index: %+v", position, item)
		}
		seen[item.ID] = true
	}
	repeated, err := m.Page(created.Queue.ID, 2, idx, 7)
	if err != nil || repeated.Items[0].ID != all[PageSize].ID {
		t.Fatalf("page order changed: %v %+v", err, repeated.Items)
	}
}

func TestPinsInsideOutsideAndEmptyTagWithoutDuplicates(t *testing.T) {
	idx := testIndex(5)
	tags := staticTags{"small": {idx.Files[1].Filepath, idx.Files[2].Filepath}, "empty": nil}
	m := NewManager(Config{}, tags, Options{Random: randomBytes(8)})

	inside, err := m.Create(context.Background(), idx, 1, CreateRequest{Tag: "small", PinFileID: idx.Files[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if !inside.PinApplied {
		t.Fatal("inside pin was not applied")
	}
	assertIDs(t, inside.Items, []string{idx.Files[2].ID, idx.Files[1].ID})

	outside, err := m.Create(context.Background(), idx, 1, CreateRequest{Tag: "small", PinFileID: idx.Files[4].ID})
	if err != nil {
		t.Fatal(err)
	}
	if outside.PinApplied || outside.Queue.Total != 2 {
		t.Fatalf("outside pin leaked into tag queue: %+v", outside)
	}
	seen := map[string]bool{idx.Files[1].ID: false, idx.Files[2].ID: false}
	for _, item := range outside.Items {
		if _, ok := seen[item.ID]; !ok {
			t.Fatalf("outside tag queue contains %q", item.ID)
		}
		seen[item.ID] = true
	}
	for id, found := range seen {
		if !found {
			t.Fatalf("tag queue omitted %q", id)
		}
	}

	empty, err := m.Create(context.Background(), idx, 1, CreateRequest{Tag: "empty", PinFileID: idx.Files[0].ID})
	if err != nil || empty.PinApplied || empty.Queue.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("empty tag pin = %+v/%v", empty, err)
	}
}

func TestRescanKeepsQueueAndMarksAvailability(t *testing.T) {
	m := NewManager(Config{}, nil, Options{Random: randomBytes(3)})
	original := testIndex(3)
	created, err := m.Create(context.Background(), original, 1, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	removedID := created.Items[1].ID
	current := &index.Index{ByID: make(map[string]*index.FileEntry)}
	for i := range original.Files {
		if original.Files[i].ID == removedID {
			continue
		}
		current.Files = append(current.Files, original.Files[i])
	}
	newPath := "new.flac"
	current.Files = append(current.Files, index.FileEntry{ID: index.GenerateID(newPath), Filepath: newPath, Name: "new", Dir: "."})
	for i := range current.Files {
		current.ByID[current.Files[i].ID] = &current.Files[i]
	}
	page, err := m.Page(created.Queue.ID, 1, current, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Queue.ID != created.Queue.ID || page.Queue.Total != created.Queue.Total || page.Items[1].Available {
		t.Fatalf("rescan mutated queue identity/order instead of availability: %+v", page)
	}
	for _, item := range page.Items {
		if item.ID == current.Files[len(current.Files)-1].ID {
			t.Fatal("newly scanned file was inserted into an existing queue")
		}
	}
}

func TestQueuesShareSnapshotAndOrderStorageIsFourBytesPerCandidate(t *testing.T) {
	idx := testIndex(100_000)
	m := NewManager(Config{MaxBytes: 1 << 30}, nil, Options{Random: randomBytes(4)})
	first, err := m.Create(context.Background(), idx, 9, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	afterFirst := m.Stats()
	second, err := m.Create(context.Background(), idx, 9, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	afterSecond := m.Stats()
	if afterSecond.Snapshots != 1 || afterSecond.Queues != 2 {
		t.Fatalf("snapshot was copied per queue: %+v", afterSecond)
	}
	delta := afterSecond.Bytes - afterFirst.Bytes
	if delta > int64(len(idx.Files))*4+1024 {
		t.Fatalf("second queue added %d bytes, expected uint32 order plus small management overhead", delta)
	}
	m.Delete(first.Queue.ID)
	if m.Stats().Snapshots != 1 {
		t.Fatal("shared snapshot released while another queue still referenced it")
	}
	m.Delete(second.Queue.ID)
	if stats := m.Stats(); stats.Snapshots != 0 || stats.Bytes != 0 {
		t.Fatalf("unreferenced generation was not reclaimed: %+v", stats)
	}
}

func TestSelectExistingOrAtomicallyReplacesQueue(t *testing.T) {
	idx := testIndex(4)
	tags := staticTags{"one": {idx.Files[0].Filepath}}
	m := NewManager(Config{}, tags, Options{Random: randomBytes(8)})
	created, err := m.Create(context.Background(), idx, 3, CreateRequest{Tag: "one"})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := m.Select(context.Background(), created.Queue.ID, idx.Files[0].ID, idx, 3)
	if err != nil || existing.Queue.ID != created.Queue.ID || existing.QueueIndex != 0 {
		t.Fatalf("existing select = %+v/%v", existing, err)
	}
	replacement, err := m.Select(context.Background(), created.Queue.ID, idx.Files[3].ID, idx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Queue.ID == created.Queue.ID || replacement.QueueIndex != 0 || replacement.Items[0].ID != idx.Files[3].ID || replacement.Items[1].ID != idx.Files[0].ID {
		t.Fatalf("replacement did not preserve remaining order: %+v", replacement)
	}
	if _, err := m.Page(created.Queue.ID, 1, idx, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old queue remained after atomic replacement: %v", err)
	}
}

func TestSelectLargeQueueHonorsCancellationWithoutRemovingQueue(t *testing.T) {
	idx := testIndex(5_000)
	tagged := make([]string, 0, len(idx.Files)-1)
	for i := 0; i < len(idx.Files)-1; i++ {
		tagged = append(tagged, idx.Files[i].Filepath)
	}
	m := NewManager(Config{MaxBytes: 1 << 30}, staticTags{"most": tagged}, Options{Random: randomBytes(4)})
	created, err := m.Create(context.Background(), idx, 1, CreateRequest{Tag: "most"})
	if err != nil {
		t.Fatal(err)
	}

	ctx := &cancelAfterChecks{remaining: 3}
	if _, err := m.Select(ctx, created.Queue.ID, idx.Files[len(idx.Files)-1].ID, idx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled select error = %v", err)
	}
	if _, err := m.Page(created.Queue.ID, 1, idx, 1); err != nil {
		t.Fatalf("canceled select removed the original queue: %v", err)
	}
}

func TestTTLDeleteForgeryLRUAndCapacity(t *testing.T) {
	now := time.Unix(100, 0)
	m := NewManager(Config{MaxQueues: 2, MaxBytes: 1 << 20, Idle: time.Minute}, nil, Options{
		Random: randomBytes(8), Now: func() time.Time { return now },
	})
	idx := testIndex(2)
	first, _ := m.Create(context.Background(), idx, 1, CreateRequest{})
	now = now.Add(time.Second)
	second, _ := m.Create(context.Background(), idx, 1, CreateRequest{})
	now = now.Add(time.Second)
	if _, err := m.Page(first.Queue.ID, 1, idx, 1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	third, _ := m.Create(context.Background(), idx, 1, CreateRequest{})
	if _, err := m.Page(second.Queue.ID, 1, idx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("least recently used queue was not evicted: %v", err)
	}
	if _, err := m.Page(third.Queue.ID+"x", 1, idx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forged token error = %v", err)
	}
	m.Delete(third.Queue.ID)
	m.Delete(third.Queue.ID)
	now = now.Add(2 * time.Minute)
	if _, err := m.Page(first.Queue.ID, 1, idx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired queue error = %v", err)
	}

	tiny := NewManager(Config{MaxQueues: 1, MaxBytes: 100, Idle: time.Hour}, nil, Options{Random: randomBytes(2)})
	if _, err := tiny.Create(context.Background(), idx, 1, CreateRequest{}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized single queue error = %v", err)
	}
}

func TestBuildGateCancellationReleasesWaiter(t *testing.T) {
	gate := newBuildGate(1, 1, time.Second)
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gate.acquire(ctx) }()
	deadline := time.Now().Add(time.Second)
	for len(gate.waiters) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(gate.waiters) != 1 {
		t.Fatal("waiting build did not occupy the bounded queue")
	}
	if err := gate.acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("full build wait queue error = %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v", err)
	}
	gate.release()
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatalf("waiter position leaked: %v", err)
	}
	gate.release()
}

func BenchmarkQueueShuffle(b *testing.B) {
	for _, size := range []int{10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			base := make([]uint32, size)
			for i := range base {
				base[i] = uint32(i)
			}
			var seed [32]byte
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				order := append([]uint32(nil), base...)
				seed[0] = byte(i)
				shuffle(order, seed)
			}
		})
	}
}

func BenchmarkQueueBuild(b *testing.B) {
	for _, size := range []int{10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			idx := testIndex(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m := NewManager(Config{MaxBytes: 1 << 40}, nil)
				if _, err := m.Create(context.Background(), idx, 1, CreateRequest{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func assertIDs(t *testing.T, items []Item, expected []string) {
	t.Helper()
	if len(items) != len(expected) {
		t.Fatalf("items = %d, want %d", len(items), len(expected))
	}
	for i := range expected {
		if items[i].ID != expected[i] {
			t.Fatalf("item %d = %q, want %q", i, items[i].ID, expected[i])
		}
	}
}

func equalUint32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
