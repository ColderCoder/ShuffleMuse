package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

const (
	sessionCookieName  = "shufflemuse-session"
	defaultMaxSessions = 1024
)

type session struct {
	created    time.Time
	expires    time.Time
	lastAccess time.Time
}

// Auth owns server-side sessions. Only a SHA-256 digest of each random token
// is retained, so a database or memory disclosure does not reveal live cookie
// values.
type Auth struct {
	password  string
	secure    bool
	whitelist []netip.Prefix
	sessions  map[[sha256.Size]byte]session
	max       int
	now       func() time.Time
	mu        sync.Mutex
}

func New(password string, whitelist ...netip.Prefix) *Auth {
	return &Auth{
		password:  password,
		whitelist: append([]netip.Prefix(nil), whitelist...),
		sessions:  make(map[[sha256.Size]byte]session),
		max:       defaultMaxSessions,
		now:       time.Now,
	}
}

func (a *Auth) SetCookieSecure(secure bool) {
	a.mu.Lock()
	a.secure = secure
	a.mu.Unlock()
}

func (a *Auth) IsWhitelisted(r *http.Request) bool {
	if len(a.whitelist) == 0 {
		return false
	}
	address, ok := DirectPeerIP(r)
	if !ok {
		return false
	}
	for _, prefix := range a.whitelist {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (a *Auth) Allows(r *http.Request) bool {
	return a.IsWhitelisted(r) || a.Validate(r) == nil
}

func (a *Auth) Login(password string, remember bool) (*http.Cookie, error) {
	if subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) != 1 {
		return nil, errors.New("invalid credentials")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, errors.New("generate session token")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	maxAge := time.Hour
	if remember {
		maxAge = 30 * 24 * time.Hour
	}

	a.mu.Lock()
	now := a.now()
	a.removeExpiredLocked(now)
	for len(a.sessions) >= a.max {
		a.evictOldestLocked()
	}
	a.sessions[digest] = session{created: now, expires: now.Add(maxAge), lastAccess: now}
	secure := a.secure
	a.mu.Unlock()

	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		MaxAge:   int(maxAge / time.Second),
		Expires:  now.Add(maxAge),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		Secure:   secure,
	}, nil
}

func (a *Auth) Validate(r *http.Request) error {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return errors.New("no session cookie")
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	current, ok := a.sessions[digest]
	if !ok {
		return errors.New("unknown session")
	}
	if !now.Before(current.expires) {
		delete(a.sessions, digest)
		return errors.New("session expired")
	}
	current.lastAccess = now
	a.sessions[digest] = current
	return nil
}

func (a *Auth) Logout(r *http.Request) *http.Cookie {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		digest := sha256.Sum256([]byte(cookie.Value))
		a.mu.Lock()
		delete(a.sessions, digest)
		a.mu.Unlock()
	}
	a.mu.Lock()
	secure := a.secure
	a.mu.Unlock()
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		Secure:   secure,
	}
}

func (a *Auth) removeExpiredLocked(now time.Time) {
	for digest, current := range a.sessions {
		if !now.Before(current.expires) {
			delete(a.sessions, digest)
		}
	}
}

func (a *Auth) evictOldestLocked() {
	var oldestDigest [sha256.Size]byte
	var oldest session
	found := false
	for digest, current := range a.sessions {
		if !found || current.lastAccess.Before(oldest.lastAccess) ||
			(current.lastAccess.Equal(oldest.lastAccess) && current.created.Before(oldest.created)) {
			oldestDigest, oldest, found = digest, current, true
		}
	}
	if found {
		delete(a.sessions, oldestDigest)
	}
}
