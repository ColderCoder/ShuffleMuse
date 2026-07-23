package auth

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"
)

const testPassword = "correct-password"

func TestWhitelistUsesDirectPeerAddress(t *testing.T) {
	a := New(testPassword, netip.MustParsePrefix("192.168.50.0/24"), netip.MustParsePrefix("::1/128"))
	allowed := httptest.NewRequest(http.MethodGet, "/", nil)
	allowed.RemoteAddr = "192.168.50.23:12345"
	if !a.IsWhitelisted(allowed) || !a.Allows(allowed) {
		t.Fatal("expected direct peer inside whitelist to bypass authentication")
	}
	outside := httptest.NewRequest(http.MethodGet, "/", nil)
	outside.RemoteAddr = "203.0.113.10:12345"
	outside.Header.Set("X-Forwarded-For", "192.168.50.23")
	if a.IsWhitelisted(outside) || a.Allows(outside) {
		t.Fatal("forwarded headers must not affect the authentication whitelist")
	}
}

func TestSessionLifecycleAndCookieAttributes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := New(testPassword)
	a.now = func() time.Time { return now }
	a.SetCookieSecure(true)
	cookie, err := a.Login(testPassword, false)
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != sessionCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 3600 {
		t.Fatalf("unexpected session cookie: %+v", cookie)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	if err := a.Validate(req); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	now = now.Add(time.Hour)
	if err := a.Validate(req); err == nil {
		t.Fatal("expired session was accepted")
	}
	if len(a.sessions) != 0 {
		t.Fatalf("expired session was not removed: %d", len(a.sessions))
	}
}

func TestLoginWrongPasswordDoesNotCreateSession(t *testing.T) {
	a := New(testPassword)
	if _, err := a.Login("wrong-password", false); err == nil {
		t.Fatal("wrong password should fail")
	}
	if len(a.sessions) != 0 {
		t.Fatal("wrong password created a session")
	}
}

func TestRememberMeExpiry(t *testing.T) {
	a := New(testPassword)
	cookie, err := a.Login(testPassword, true)
	if err != nil {
		t.Fatal(err)
	}
	if cookie.MaxAge != 30*24*60*60 {
		t.Fatalf("remember MaxAge = %d", cookie.MaxAge)
	}
}

func TestSessionCapacityEvictsLeastRecentlyUsed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := New(testPassword)
	a.max = 2
	a.now = func() time.Time { return now }
	first, _ := a.Login(testPassword, false)
	now = now.Add(time.Second)
	second, _ := a.Login(testPassword, false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(first)
	now = now.Add(time.Second)
	if err := a.Validate(req); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	third, _ := a.Login(testPassword, false)
	if len(a.sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(a.sessions))
	}
	secondReq := httptest.NewRequest(http.MethodGet, "/", nil)
	secondReq.AddCookie(second)
	if err := a.Validate(secondReq); err == nil {
		t.Fatal("least recently used session was not evicted")
	}
	thirdReq := httptest.NewRequest(http.MethodGet, "/", nil)
	thirdReq.AddCookie(third)
	if err := a.Validate(thirdReq); err != nil {
		t.Fatal("new session was evicted")
	}
}

func TestLogoutOnlyRemovesKnownSession(t *testing.T) {
	a := New(testPassword)
	known, _ := a.Login(testPassword, false)
	forged := httptest.NewRequest(http.MethodPost, "/", nil)
	forged.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "forged"})
	a.Logout(forged)
	if len(a.sessions) != 1 {
		t.Fatalf("forged logout changed session count: %d", len(a.sessions))
	}
	knownReq := httptest.NewRequest(http.MethodPost, "/", nil)
	knownReq.AddCookie(known)
	cleared := a.Logout(knownReq)
	if len(a.sessions) != 0 || cleared.MaxAge != -1 || cleared.Value != "" {
		t.Fatalf("known logout failed: sessions=%d cookie=%+v", len(a.sessions), cleared)
	}
}

func TestClientIPResolverModesAndTrustBoundary(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.0/24"),
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "10.0.0.5:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.10")
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")

	remote, _ := NewClientIPResolver("remote", trusted).ClientIP(request)
	if remote.String() != "10.0.0.5" {
		t.Fatalf("remote mode returned %s", remote)
	}
	xff, _ := NewClientIPResolver("x-forwarded-for", trusted).ClientIP(request)
	if xff.String() != "198.51.100.7" {
		t.Fatalf("XFF mode returned %s", xff)
	}
	cf, _ := NewClientIPResolver("cf-connecting-ip", trusted).ClientIP(request)
	if cf.String() != "203.0.113.9" {
		t.Fatalf("CF mode returned %s", cf)
	}

	untrusted := request.Clone(request.Context())
	untrusted.RemoteAddr = "198.51.100.20:1234"
	spoofed, _ := NewClientIPResolver("x-forwarded-for", trusted).ClientIP(untrusted)
	if spoofed.String() != "198.51.100.20" {
		t.Fatalf("untrusted peer overrode address: %s", spoofed)
	}
}

func TestClientIPResolverMalformedHeadersFallBackToPeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct{ mode, header, value string }{
		{"x-forwarded-for", "X-Forwarded-For", "bad, 192.0.2.1"},
		{"cf-connecting-ip", "CF-Connecting-IP", "198.51.100.1, 198.51.100.2"},
		{"cf-connecting-ip", "CF-Connecting-IP", "bad"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "10.0.0.4:80"
		req.Header.Set(tc.header, tc.value)
		got, _ := NewClientIPResolver(tc.mode, trusted).ClientIP(req)
		if got.String() != "10.0.0.4" {
			t.Fatalf("%s malformed header returned %s", tc.mode, got)
		}
	}
}

func TestClientIPResolverAllTrustedChainFallsBackToPeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.4:80"
	req.Header.Set("X-Forwarded-For", "10.1.0.2, 10.2.0.3")
	got, _ := NewClientIPResolver("x-forwarded-for", trusted).ClientIP(req)
	if got.String() != "10.0.0.4" {
		t.Fatalf("all-trusted chain returned %s, want direct peer", got)
	}
}

func TestLoginGuardThresholdExpirySuccessAndFixedDeadline(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	guard := NewLoginGuard(3, time.Hour)
	if defaultMaxLoginFailureEntries != 4096 {
		t.Fatalf("declared default capacity = %d, want 4096", defaultMaxLoginFailureEntries)
	}
	if guard.maxEntries != defaultMaxLoginFailureEntries {
		t.Fatalf("default capacity = %d, want 4096", guard.maxEntries)
	}
	guard.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if _, blocked := guard.Failure("client-a"); blocked {
			t.Fatalf("blocked after %d failures", i+1)
		}
	}
	retry, blocked := guard.Failure("client-a")
	if !blocked || retry != time.Hour {
		t.Fatalf("third failure retry/blocked = %v/%v", retry, blocked)
	}
	now = now.Add(10 * time.Minute)
	retry, blocked = guard.Failure("client-a")
	if !blocked || retry != 50*time.Minute {
		t.Fatalf("continued attack extended deadline: %v/%v", retry, blocked)
	}
	if _, blocked := guard.Check("client-b"); blocked {
		t.Fatal("client IPs were not isolated")
	}
	now = now.Add(50 * time.Minute)
	if _, blocked := guard.Check("client-a"); blocked {
		t.Fatal("ban did not expire")
	}
	guard.Failure("client-a")
	guard.Success("client-a")
	if _, ok := guard.failures["client-a"]; ok {
		t.Fatal("successful login did not clear failures")
	}
}

func TestLoginGuardCapacityPrefersEvictingPartialFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	guard := NewLoginGuard(2, time.Hour)
	guard.maxEntries = 3
	guard.now = func() time.Time { return now }

	guard.Failure("blocked")
	guard.Failure("blocked")
	now = now.Add(time.Second)
	guard.Failure("partial-oldest")
	now = now.Add(time.Second)
	guard.Failure("partial-newest")
	now = now.Add(time.Second)
	guard.Failure("new-client")

	if len(guard.failures) != guard.maxEntries {
		t.Fatalf("failure state count = %d, want hard limit %d", len(guard.failures), guard.maxEntries)
	}
	if _, ok := guard.failures["partial-oldest"]; ok {
		t.Fatal("least recently used partial failure was not evicted")
	}
	if _, ok := guard.failures["blocked"]; !ok {
		t.Fatal("active ban was evicted while partial failure records existed")
	}
	if retry, blocked := guard.Check("blocked"); !blocked || retry != time.Hour-3*time.Second {
		t.Fatalf("retained ban changed: retry/blocked = %v/%v", retry, blocked)
	}
}

func TestLoginGuardCapacityDoesNotEvictActiveBans(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	guard := NewLoginGuard(1, time.Hour)
	guard.maxEntries = 2
	guard.now = func() time.Time { return now }

	guard.Failure("oldest")
	now = now.Add(time.Second)
	guard.Failure("newest")
	now = now.Add(time.Second)
	retry, blocked := guard.Failure("incoming")

	if len(guard.failures) != guard.maxEntries {
		t.Fatalf("failure state count = %d, want hard limit %d", len(guard.failures), guard.maxEntries)
	}
	if !blocked || retry != time.Hour {
		t.Fatalf("saturated failure retry/blocked = %v/%v", retry, blocked)
	}
	for _, client := range []string{"oldest", "newest"} {
		if _, ok := guard.failures[client]; !ok {
			t.Fatalf("active ban for %q was evicted", client)
		}
	}
	if _, ok := guard.failures["incoming"]; ok {
		t.Fatal("saturated guard retained an additional client key")
	}
	if _, blocked := guard.Check("incoming"); blocked {
		t.Fatal("untracked saturated client remained blocked before password verification")
	}
}

func TestLoginGuardCapacityPurgesExpiredBansBeforeAdmission(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	guard := NewLoginGuard(1, time.Hour)
	guard.maxEntries = 2
	guard.now = func() time.Time { return now }

	guard.Failure("expired")
	now = now.Add(30 * time.Minute)
	guard.Failure("active")
	now = now.Add(30 * time.Minute)
	retry, blocked := guard.Failure("incoming")

	if !blocked || retry != time.Hour {
		t.Fatalf("incoming failure retry/blocked = %v/%v", retry, blocked)
	}
	if len(guard.failures) != guard.maxEntries {
		t.Fatalf("failure state count = %d, want %d", len(guard.failures), guard.maxEntries)
	}
	if _, ok := guard.failures["expired"]; ok {
		t.Fatal("expired ban was not purged at capacity")
	}
	for _, client := range []string{"active", "incoming"} {
		if _, ok := guard.failures[client]; !ok {
			t.Fatalf("expected %q to remain tracked", client)
		}
	}
}

func TestLoginGuardConcurrentCapacityAndStateConsistency(t *testing.T) {
	guard := NewLoginGuard(3, time.Hour)
	guard.maxEntries = 64

	const workers = 32
	const operations = 500
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for operation := 0; operation < operations; operation++ {
				client := "client-" + strconv.Itoa((worker*operations+operation)%512)
				switch operation % 4 {
				case 0:
					guard.Check(client)
				case 1, 2:
					guard.Failure(client)
				case 3:
					guard.Success(client)
				}
			}
		}(worker)
	}
	wg.Wait()

	guard.mu.Lock()
	defer guard.mu.Unlock()
	if len(guard.failures) > guard.maxEntries {
		t.Fatalf("failure state count = %d, exceeds hard limit %d", len(guard.failures), guard.maxEntries)
	}
	if got := guard.pendingLRU.Len() + guard.blockedLRU.Len(); got != len(guard.failures) {
		t.Fatalf("LRU entries = %d, failure states = %d", got, len(guard.failures))
	}
	for client, current := range guard.failures {
		if current.element == nil || current.element.Value != client {
			t.Fatalf("inconsistent LRU entry for %q", client)
		}
	}
}
