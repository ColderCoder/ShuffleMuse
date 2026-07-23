package auth

import (
	"container/list"
	"sync"
	"time"
)

// defaultMaxLoginFailureEntries bounds the number of distinct client
// addresses retained by the in-memory login guard. The limit is deliberately
// fixed rather than configurable: it is a safety bound, not a tuning knob.
const defaultMaxLoginFailureEntries = 4096

type loginFailure struct {
	count        int
	blockedUntil time.Time
	element      *list.Element
}

type LoginGuard struct {
	maxFailures int
	banDuration time.Duration
	maxEntries  int
	failures    map[string]*loginFailure
	pendingLRU  list.List
	blockedLRU  list.List
	now         func() time.Time
	mu          sync.Mutex
}

func NewLoginGuard(maxFailures int, banDuration time.Duration) *LoginGuard {
	return &LoginGuard{
		maxFailures: maxFailures,
		banDuration: banDuration,
		maxEntries:  defaultMaxLoginFailureEntries,
		failures:    make(map[string]*loginFailure),
		now:         time.Now,
	}
}

func (g *LoginGuard) Check(client string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	current, ok := g.failures[client]
	if !ok || current.blockedUntil.IsZero() {
		if ok {
			g.pendingLRU.MoveToFront(current.element)
		}
		return 0, false
	}
	now := g.now()
	if !now.Before(current.blockedUntil) {
		g.removeLocked(client, current)
		return 0, false
	}
	g.blockedLRU.MoveToFront(current.element)
	return current.blockedUntil.Sub(now), true
}

func (g *LoginGuard) Failure(client string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	current, exists := g.failures[client]
	if exists && !current.blockedUntil.IsZero() && now.Before(current.blockedUntil) {
		g.blockedLRU.MoveToFront(current.element)
		return current.blockedUntil.Sub(now), true
	}
	if !exists {
		if !g.makeRoomLocked(now) {
			// Capacity is occupied entirely by active bans. Do not evict one:
			// reject this failed attempt without retaining another client key.
			// A later request with the correct password can still proceed because
			// Check does not treat an untracked address as blocked.
			return g.banDuration, true
		}
		current = &loginFailure{}
		current.element = g.pendingLRU.PushFront(client)
		g.failures[client] = current
	} else if !current.blockedUntil.IsZero() {
		g.blockedLRU.Remove(current.element)
		current.count = 0
		current.blockedUntil = time.Time{}
		current.element = g.pendingLRU.PushFront(client)
	} else {
		g.pendingLRU.MoveToFront(current.element)
	}
	current.count++
	if current.count >= g.maxFailures {
		current.blockedUntil = now.Add(g.banDuration)
		g.pendingLRU.Remove(current.element)
		current.element = g.blockedLRU.PushFront(client)
	}
	if current.blockedUntil.IsZero() {
		return 0, false
	}
	return g.banDuration, true
}

func (g *LoginGuard) Success(client string) {
	g.mu.Lock()
	if current, ok := g.failures[client]; ok {
		g.removeLocked(client, current)
	}
	g.mu.Unlock()
}

// makeRoomLocked enforces the hard global capacity. Its linear cleanup scan is
// only performed when a new client arrives at capacity. Expired bans are
// removed first, followed by the least recently used partial failure. Active
// bans are never evicted: if they occupy every slot, the caller rejects the
// current failed attempt without retaining a new client key.
func (g *LoginGuard) makeRoomLocked(now time.Time) bool {
	if g.maxEntries < 1 {
		g.maxEntries = defaultMaxLoginFailureEntries
	}
	if len(g.failures) < g.maxEntries {
		return true
	}
	for element := g.blockedLRU.Back(); element != nil; {
		previous := element.Prev()
		client := element.Value.(string)
		current := g.failures[client]
		if !now.Before(current.blockedUntil) {
			g.removeLocked(client, current)
		}
		element = previous
	}
	if len(g.failures) < g.maxEntries {
		return true
	}
	candidate := g.pendingLRU.Back()
	if candidate == nil {
		return false
	}
	client := candidate.Value.(string)
	g.removeLocked(client, g.failures[client])
	return true
}

func (g *LoginGuard) removeLocked(client string, current *loginFailure) {
	if current.blockedUntil.IsZero() {
		g.pendingLRU.Remove(current.element)
	} else {
		g.blockedLRU.Remove(current.element)
	}
	delete(g.failures, client)
}
