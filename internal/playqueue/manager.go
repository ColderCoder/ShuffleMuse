package playqueue

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"math"
	rand "math/rand/v2"
	"sync"
	"time"
	"unsafe"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
)

const (
	PageSize           = 200
	defaultMaxQueues   = 64
	defaultMaxBytes    = 128 << 20
	defaultIdle        = 24 * time.Hour
	defaultBuildSlots  = 2
	defaultBuildWaiter = 4
	defaultBuildWait   = 5 * time.Second
)

var (
	ErrNotFound     = errors.New("queue not found")
	ErrFileNotFound = errors.New("file not found")
	ErrBusy         = errors.New("queue builder is busy")
	ErrCapacity     = errors.New("queue cache capacity exceeded")
)

type TagSource interface {
	GetFilesByTag(tag string) ([]string, error)
}

type Config struct {
	MaxQueues  int
	MaxBytes   int64
	Idle       time.Duration
	BuildSlots int
	BuildQueue int
	BuildWait  time.Duration
}

type Options struct {
	Random io.Reader
	Now    func() time.Time
}

type Description struct {
	ID                string `json:"id"`
	Tag               string `json:"tag"`
	CreatedGeneration uint64 `json:"createdGeneration"`
	Total             int    `json:"total"`
	PageSize          int    `json:"pageSize"`
}

type Item struct {
	ID         string `json:"id"`
	Filepath   string `json:"filepath"`
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	QueueIndex int    `json:"queueIndex"`
	Available  bool   `json:"available"`
}

type Page struct {
	Queue             Description `json:"queue"`
	Items             []Item      `json:"items"`
	Page              int         `json:"page"`
	LibraryGeneration uint64      `json:"libraryGeneration"`
}

type CreateRequest struct {
	Tag            string
	PinFileID      string
	ReplaceQueueID string
}

type CreateResult struct {
	Page
	PinApplied bool `json:"pinApplied"`
}

type SelectResult struct {
	Page
	QueueIndex int `json:"queueIndex"`
}

type snapshotRef struct {
	generation uint64
	index      *index.Index
	refs       int
	bytes      int64
}

type queue struct {
	digest            [sha256.Size]byte
	tag               string
	createdGeneration uint64
	snapshot          *snapshotRef
	order             []uint32
	prefix            []index.FileEntry
	createdAt         time.Time
	lastAccess        time.Time
	bytes             int64
}

func (q *queue) total() int {
	return len(q.prefix) + len(q.order)
}

type buildGate struct {
	slots   chan struct{}
	waiters chan struct{}
	wait    time.Duration
}

func newBuildGate(slots, waiters int, wait time.Duration) *buildGate {
	return &buildGate{
		slots: make(chan struct{}, slots), waiters: make(chan struct{}, waiters), wait: wait,
	}
}

func (g *buildGate) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case g.waiters <- struct{}{}:
		defer func() { <-g.waiters }()
	default:
		return ErrBusy
	}
	timer := time.NewTimer(g.wait)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-g.slots
			return err
		}
		return nil
	case <-timer.C:
		return ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *buildGate) release() { <-g.slots }

type Manager struct {
	mu         sync.Mutex
	randomMu   sync.Mutex
	queues     map[[sha256.Size]byte]*queue
	snapshots  map[uint64]*snapshotRef
	totalBytes int64
	config     Config
	tags       TagSource
	random     io.Reader
	now        func() time.Time
	builds     *buildGate
}

func NewManager(config Config, tags TagSource, options ...Options) *Manager {
	if config.MaxQueues <= 0 {
		config.MaxQueues = defaultMaxQueues
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = defaultMaxBytes
	}
	if config.Idle <= 0 {
		config.Idle = defaultIdle
	}
	if config.BuildSlots <= 0 {
		config.BuildSlots = defaultBuildSlots
	}
	if config.BuildQueue < 0 {
		config.BuildQueue = defaultBuildWaiter
	}
	if config.BuildQueue == 0 && config.BuildSlots == defaultBuildSlots {
		config.BuildQueue = defaultBuildWaiter
	}
	if config.BuildWait <= 0 {
		config.BuildWait = defaultBuildWait
	}
	option := Options{Random: cryptorand.Reader, Now: time.Now}
	if len(options) > 0 {
		if options[0].Random != nil {
			option.Random = options[0].Random
		}
		if options[0].Now != nil {
			option.Now = options[0].Now
		}
	}
	return &Manager{
		queues: make(map[[sha256.Size]byte]*queue), snapshots: make(map[uint64]*snapshotRef),
		config: config, tags: tags, random: option.Random, now: option.Now,
		builds: newBuildGate(config.BuildSlots, config.BuildQueue, config.BuildWait),
	}
}

func (m *Manager) Create(ctx context.Context, current *index.Index, generation uint64, request CreateRequest) (CreateResult, error) {
	if current == nil {
		return CreateResult{}, ErrFileNotFound
	}
	if err := m.builds.acquire(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CreateResult{}, err
		}
		return CreateResult{}, ErrBusy
	}
	defer m.builds.release()

	canonical := m.canonicalSnapshot(generation, current)
	order, prefix, pinApplied, err := m.buildOrder(ctx, canonical, request.Tag, request.PinFileID)
	if err != nil {
		return CreateResult{}, err
	}
	var seed [32]byte
	if err := m.readRandom(seed[:]); err != nil {
		return CreateResult{}, err
	}
	shuffle(order, seed)
	token, digest, err := m.newToken()
	if err != nil {
		return CreateResult{}, err
	}
	now := m.now()
	created := &queue{
		digest: digest, tag: request.Tag, createdGeneration: generation,
		order: order, prefix: prefix, createdAt: now, lastAccess: now,
	}
	created.bytes = estimateQueue(created)
	snapshotBytes := estimateSnapshot(canonical)
	if saturatedAdd(snapshotBytes, created.bytes) > m.config.MaxBytes {
		return CreateResult{}, ErrCapacity
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(now)
	if _, exists := m.queues[digest]; exists {
		return CreateResult{}, errors.New("queue token collision")
	}
	var replaced *queue
	if request.ReplaceQueueID != "" {
		replaceDigest, ok := decodeToken(request.ReplaceQueueID)
		if !ok {
			return CreateResult{}, ErrNotFound
		}
		replaced = m.queues[replaceDigest]
		if replaced == nil {
			return CreateResult{}, ErrNotFound
		}
		m.removeLocked(replaced)
	}
	created.snapshot = m.retainSnapshotLocked(generation, canonical, snapshotBytes)
	m.queues[digest] = created
	m.totalBytes = saturatedAdd(m.totalBytes, created.bytes)
	m.evictLocked(digest)

	page := m.pageLocked(created, token, 1, current, generation)
	_ = replaced // replacement is intentionally unreachable only after insertion succeeds.
	return CreateResult{Page: page, PinApplied: pinApplied}, nil
}

func (m *Manager) Page(token string, page int, current *index.Index, generation uint64) (Page, error) {
	if page < 1 {
		return Page{}, ErrNotFound
	}
	digest, ok := decodeToken(token)
	if !ok {
		return Page{}, ErrNotFound
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[digest]
	if q == nil || now.Sub(q.lastAccess) >= m.config.Idle {
		if q != nil {
			m.removeLocked(q)
		}
		return Page{}, ErrNotFound
	}
	q.lastAccess = now
	return m.pageLocked(q, token, page, current, generation), nil
}

func (m *Manager) Select(ctx context.Context, token, fileID string, current *index.Index, generation uint64) (SelectResult, error) {
	if err := ctx.Err(); err != nil {
		return SelectResult{}, err
	}
	digest, ok := decodeToken(token)
	if !ok {
		return SelectResult{}, ErrNotFound
	}
	if current == nil {
		return SelectResult{}, ErrFileNotFound
	}
	selected, online := current.ByID[fileID]
	if !online {
		return SelectResult{}, ErrFileNotFound
	}
	now := m.now()
	m.mu.Lock()
	q := m.queues[digest]
	if q == nil || now.Sub(q.lastAccess) >= m.config.Idle {
		if q != nil {
			m.removeLocked(q)
		}
		m.mu.Unlock()
		return SelectResult{}, ErrNotFound
	}
	q.lastAccess = now
	m.mu.Unlock()

	// Queue contents are immutable while published. Scan outside the manager
	// lock so a large queue cannot block unrelated page, create, or delete
	// operations. The identity is checked again before returning or replacing.
	position, err := queuePosition(ctx, q, fileID)
	if err != nil {
		return SelectResult{}, err
	}
	if position >= 0 {
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return SelectResult{}, err
		}
		if m.queues[digest] != q {
			return SelectResult{}, ErrNotFound
		}
		q.lastAccess = m.now()
		pageNumber := position/PageSize + 1
		return SelectResult{Page: m.pageLocked(q, token, pageNumber, current, generation), QueueIndex: position}, nil
	}

	newToken, newDigest, err := m.newToken()
	if err != nil {
		return SelectResult{}, err
	}
	prefix := make([]index.FileEntry, 0, len(q.prefix)+1)
	prefix = append(prefix, *selected)
	prefix = append(prefix, q.prefix...)
	replacement := &queue{
		digest: newDigest, tag: q.tag, createdGeneration: q.createdGeneration,
		snapshot: q.snapshot, order: q.order, prefix: prefix,
		createdAt: now, lastAccess: now,
	}
	replacement.bytes = estimateQueue(replacement)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SelectResult{}, err
	}
	if m.queues[digest] != q {
		return SelectResult{}, ErrNotFound
	}
	if _, exists := m.queues[newDigest]; exists {
		return SelectResult{}, errors.New("queue token collision")
	}
	if saturatedAdd(q.snapshot.bytes, replacement.bytes) > m.config.MaxBytes {
		return SelectResult{}, ErrCapacity
	}
	// Transfer logical ownership of the shared snapshot and immutable order
	// without copying them. The detached queue may still be held briefly by a
	// concurrent read-only scan, so its fields are deliberately not cleared.
	m.totalBytes -= q.bytes
	delete(m.queues, q.digest)
	m.queues[newDigest] = replacement
	m.totalBytes = saturatedAdd(m.totalBytes, replacement.bytes)
	m.evictLocked(newDigest)
	return SelectResult{Page: m.pageLocked(replacement, newToken, 1, current, generation), QueueIndex: 0}, nil
}

func (m *Manager) Delete(token string) {
	digest, ok := decodeToken(token)
	if !ok {
		return
	}
	m.mu.Lock()
	if q := m.queues[digest]; q != nil {
		m.removeLocked(q)
	}
	m.mu.Unlock()
}

func (m *Manager) canonicalSnapshot(generation uint64, current *index.Index) *index.Index {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snapshot := m.snapshots[generation]; snapshot != nil {
		return snapshot.index
	}
	return current
}

func (m *Manager) buildOrder(ctx context.Context, snapshot *index.Index, tag, pinID string) ([]uint32, []index.FileEntry, bool, error) {
	var tagged map[string]struct{}
	if tag != "" {
		if m.tags == nil {
			return nil, nil, false, errors.New("tag store is unavailable")
		}
		paths, err := m.tags.GetFilesByTag(tag)
		if err != nil {
			return nil, nil, false, err
		}
		tagged = make(map[string]struct{}, len(paths))
		for _, path := range paths {
			tagged[path] = struct{}{}
		}
	}
	if len(snapshot.Files) > math.MaxUint32 {
		return nil, nil, false, ErrCapacity
	}
	var pin *index.FileEntry
	if pinID != "" {
		pin = snapshot.ByID[pinID]
		if pin == nil {
			return nil, nil, false, ErrFileNotFound
		}
		if tagged != nil {
			if _, ok := tagged[pin.Filepath]; !ok {
				pin = nil
			}
		}
	}
	capacity := len(snapshot.Files)
	if tagged != nil && len(tagged) < capacity {
		capacity = len(tagged)
	}
	order := make([]uint32, 0, capacity)
	for i := range snapshot.Files {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, false, err
			}
		}
		file := &snapshot.Files[i]
		if tagged != nil {
			if _, ok := tagged[file.Filepath]; !ok {
				continue
			}
		}
		if pin != nil && file.ID == pin.ID {
			continue
		}
		order = append(order, uint32(i))
	}
	if pin == nil {
		return order, nil, false, nil
	}
	return order, []index.FileEntry{*pin}, true, nil
}

func shuffle(order []uint32, seed [32]byte) {
	rng := rand.New(rand.NewChaCha8(seed))
	for i := len(order) - 1; i > 0; i-- {
		j := int(rng.Uint64N(uint64(i + 1)))
		order[i], order[j] = order[j], order[i]
	}
}

func (m *Manager) newToken() (string, [sha256.Size]byte, error) {
	var raw [32]byte
	if err := m.readRandom(raw[:]); err != nil {
		return "", [sha256.Size]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:]), nil
}

func (m *Manager) readRandom(destination []byte) error {
	m.randomMu.Lock()
	defer m.randomMu.Unlock()
	_, err := io.ReadFull(m.random, destination)
	return err
}

func decodeToken(token string) ([sha256.Size]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(raw), true
}

func queuePosition(ctx context.Context, q *queue, fileID string) (int, error) {
	for i := range q.prefix {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if q.prefix[i].ID == fileID {
			return i, nil
		}
	}
	for i, position := range q.order {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if int(position) < len(q.snapshot.index.Files) && q.snapshot.index.Files[position].ID == fileID {
			return len(q.prefix) + i, nil
		}
	}
	return -1, nil
}

func (m *Manager) pageLocked(q *queue, token string, page int, current *index.Index, generation uint64) Page {
	description := Description{
		ID: token, Tag: q.tag, CreatedGeneration: q.createdGeneration,
		Total: q.total(), PageSize: PageSize,
	}
	start, end := pageBounds(q.total(), page)
	items := make([]Item, 0, end-start)
	for position := start; position < end; position++ {
		entry := q.entry(position)
		if entry == nil {
			continue
		}
		available := false
		if current != nil {
			online := current.ByID[entry.ID]
			available = online != nil && online.Filepath == entry.Filepath
		}
		items = append(items, Item{
			ID: entry.ID, Filepath: entry.Filepath, Name: entry.Name, Dir: entry.Dir,
			QueueIndex: position, Available: available,
		})
	}
	return Page{Queue: description, Items: items, Page: page, LibraryGeneration: generation}
}

func (q *queue) entry(position int) *index.FileEntry {
	if position < 0 || position >= q.total() {
		return nil
	}
	if position < len(q.prefix) {
		return &q.prefix[position]
	}
	base := q.order[position-len(q.prefix)]
	if int(base) >= len(q.snapshot.index.Files) {
		return nil
	}
	return &q.snapshot.index.Files[base]
}

func pageBounds(total, page int) (int, int) {
	if total <= 0 || page < 1 {
		return 0, 0
	}
	if page-1 > total/PageSize {
		return total, total
	}
	start := (page - 1) * PageSize
	if start >= total {
		return total, total
	}
	return start, min(total, start+PageSize)
}

func (m *Manager) retainSnapshotLocked(generation uint64, idx *index.Index, bytes int64) *snapshotRef {
	snapshot := m.snapshots[generation]
	if snapshot == nil {
		snapshot = &snapshotRef{generation: generation, index: idx, bytes: bytes}
		m.snapshots[generation] = snapshot
		m.totalBytes = saturatedAdd(m.totalBytes, bytes)
	}
	snapshot.refs++
	return snapshot
}

func (m *Manager) releaseSnapshotLocked(snapshot *snapshotRef) {
	if snapshot == nil {
		return
	}
	snapshot.refs--
	if snapshot.refs == 0 {
		delete(m.snapshots, snapshot.generation)
		m.totalBytes -= snapshot.bytes
	}
}

func (m *Manager) removeLocked(q *queue) {
	delete(m.queues, q.digest)
	m.totalBytes -= q.bytes
	m.releaseSnapshotLocked(q.snapshot)
}

func (m *Manager) cleanupExpiredLocked(now time.Time) {
	for _, q := range m.queues {
		if now.Sub(q.lastAccess) >= m.config.Idle {
			m.removeLocked(q)
		}
	}
}

func (m *Manager) evictLocked(protected [sha256.Size]byte) {
	for len(m.queues) > m.config.MaxQueues || m.totalBytes > m.config.MaxBytes {
		var victim *queue
		for _, candidate := range m.queues {
			if candidate.digest == protected {
				continue
			}
			if victim == nil || candidate.lastAccess.Before(victim.lastAccess) ||
				(candidate.lastAccess.Equal(victim.lastAccess) && candidate.createdAt.Before(victim.createdAt)) {
				victim = candidate
			}
		}
		if victim == nil {
			return
		}
		m.removeLocked(victim)
	}
}

func estimateSnapshot(idx *index.Index) int64 {
	if idx == nil {
		return 0
	}
	bytes := saturatedMul(int64(len(idx.Files)), int64(unsafe.Sizeof(index.FileEntry{})))
	for i := range idx.Files {
		entry := &idx.Files[i]
		bytes = saturatedAdd(bytes, int64(len(entry.ID)+len(entry.Filepath)+len(entry.Name)+len(entry.Dir)))
	}
	return bytes
}

func estimateQueue(q *queue) int64 {
	bytes := int64(512 + len(q.tag))
	bytes = saturatedAdd(bytes, saturatedMul(int64(len(q.order)), int64(unsafe.Sizeof(uint32(0)))))
	bytes = saturatedAdd(bytes, saturatedMul(int64(len(q.prefix)), int64(unsafe.Sizeof(index.FileEntry{}))))
	for i := range q.prefix {
		entry := &q.prefix[i]
		bytes = saturatedAdd(bytes, int64(len(entry.ID)+len(entry.Filepath)+len(entry.Name)+len(entry.Dir)))
	}
	return bytes
}

func saturatedAdd(a, b int64) int64 {
	if a >= math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func saturatedMul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// Stats is intentionally small and is used by tests and operational probes.
type Stats struct {
	Queues    int
	Snapshots int
	Bytes     int64
}

func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Stats{Queues: len(m.queues), Snapshots: len(m.snapshots), Bytes: m.totalBytes}
}
