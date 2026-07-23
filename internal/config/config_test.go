package config

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear all env vars
	clearConfigEnv()

	cfg := Load()

	if cfg.Port != 8080 {
		t.Errorf("expected default Port 8080, got %d", cfg.Port)
	}
	if cfg.MusicDir != "/music" {
		t.Errorf("expected default MusicDir /music, got %s", cfg.MusicDir)
	}
	if cfg.RescanInterval != 0 {
		t.Errorf("expected default RescanInterval 0, got %v", cfg.RescanInterval)
	}
	if cfg.OpusBitrate != 160 {
		t.Errorf("expected default OpusBitrate 160, got %d", cfg.OpusBitrate)
	}
	if cfg.AuthPassword != "" {
		t.Errorf("expected default AuthPassword empty, got %s", cfg.AuthPassword)
	}
	if len(cfg.AuthWhitelist) != 0 {
		t.Errorf("expected default AuthWhitelist empty, got %v", cfg.AuthWhitelist)
	}
	if cfg.LoginMaxFailures != 3 || cfg.LoginBanDuration != time.Hour {
		t.Errorf("unexpected login defaults: %d/%v", cfg.LoginMaxFailures, cfg.LoginBanDuration)
	}
	if cfg.RealIPHeader != "remote" || len(cfg.TrustedProxySubnets) != 0 {
		t.Errorf("unexpected proxy defaults: %q/%v", cfg.RealIPHeader, cfg.TrustedProxySubnets)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure should default false")
	}
	if cfg.MediaQueueLimit != 8 || cfg.MediaAuxQueueLimit != 8 || cfg.MediaWaitTimeout != 15*time.Second || cfg.MediaTaskTimeout != 15*time.Second || cfg.MetadataCacheEntries != 4096 {
		t.Errorf("unexpected media defaults: %+v", cfg)
	}
	if cfg.FFmpegSessions != 2 || cfg.MediaAuxReserved != 1 {
		t.Errorf("unexpected media session defaults: %d/%d", cfg.FFmpegSessions, cfg.MediaAuxReserved)
	}
	if cfg.QueueCacheMaxQueues != 64 || cfg.QueueCacheBytes != 128<<20 || cfg.QueueIdle != 24*time.Hour {
		t.Errorf("unexpected queue defaults: %d/%d/%v", cfg.QueueCacheMaxQueues, cfg.QueueCacheBytes, cfg.QueueIdle)
	}
	if cfg.CoverCacheEntries != 128 || cfg.CoverCacheBytes != 64<<20 || cfg.StreamWriteIdle != time.Minute || cfg.MediaNegativeCache != 30*time.Second {
		t.Errorf("unexpected cache/stream defaults: %+v", cfg)
	}
	if cfg.BoltDBPath != "./data/tags.db" {
		t.Errorf("expected default BoltDBPath ./data/tags.db, got %s", cfg.BoltDBPath)
	}
}

func TestEnvOverride(t *testing.T) {
	clearConfigEnv()

	os.Setenv("MUSIC_PORT", "3000")
	os.Setenv("MUSIC_DIR", "/var/lib/music")
	os.Setenv("MUSIC_RESCAN_INTERVAL", "10m")
	os.Setenv("MUSIC_OPUS_BITRATE", "256")
	os.Setenv("MUSIC_PASSWORD", "secret")
	os.Setenv("MUSIC_AUTH_WHITELIST_SUBNETS", "127.0.0.1, 192.168.1.0/24")
	os.Setenv("MUSIC_LOGIN_MAX_FAILURES", "5")
	os.Setenv("MUSIC_LOGIN_BAN_SECONDS", "120")
	os.Setenv("MUSIC_TRUSTED_PROXY_SUBNETS", "10.0.0.0/8")
	os.Setenv("MUSIC_REAL_IP_HEADER", "CF-CONNECTING-IP")
	os.Setenv("MUSIC_COOKIE_SECURE", "true")
	os.Setenv("MUSIC_ALLOWED_HOSTS", "music.example.com")
	os.Setenv("MUSIC_FFMPEG_MAX_SESSIONS", "4")
	os.Setenv("MUSIC_MEDIA_AUX_RESERVED_SESSIONS", "2")
	os.Setenv("MUSIC_MEDIA_QUEUE_LIMIT", "12")
	os.Setenv("MUSIC_MEDIA_AUX_QUEUE_LIMIT", "7")
	os.Setenv("MUSIC_MEDIA_WAIT_SECONDS", "25")
	os.Setenv("MUSIC_MEDIA_TASK_SECONDS", "18")
	os.Setenv("MUSIC_STREAM_WRITE_IDLE_SECONDS", "45")
	os.Setenv("MUSIC_MEDIA_NEGATIVE_CACHE_SECONDS", "9")
	os.Setenv("MUSIC_METADATA_CACHE_ENTRIES", "2048")
	os.Setenv("MUSIC_COVER_CACHE_ENTRIES", "32")
	os.Setenv("MUSIC_COVER_CACHE_BYTES", "1048576")
	os.Setenv("MUSIC_QUEUE_CACHE_MAX_QUEUES", "12")
	os.Setenv("MUSIC_QUEUE_CACHE_BYTES", "2097152")
	os.Setenv("MUSIC_QUEUE_IDLE_SECONDS", "120")
	os.Setenv("MUSIC_LEGACY_MUSIC_ROOT", "/old/music")
	os.Setenv("MUSIC_BOLTDB_PATH", "/custom/tags.db")
	defer clearConfigEnv()

	cfg := Load()

	if cfg.Port != 3000 {
		t.Errorf("expected Port 3000, got %d", cfg.Port)
	}
	if cfg.MusicDir != "/var/lib/music" {
		t.Errorf("expected MusicDir /var/lib/music, got %s", cfg.MusicDir)
	}
	if cfg.RescanInterval != 10*time.Minute {
		t.Errorf("expected RescanInterval 10m, got %v", cfg.RescanInterval)
	}
	if cfg.OpusBitrate != 256 {
		t.Errorf("expected OpusBitrate 256, got %d", cfg.OpusBitrate)
	}
	if cfg.AuthPassword != "secret" {
		t.Errorf("expected AuthPassword secret, got %s", cfg.AuthPassword)
	}
	if len(cfg.AuthWhitelist) != 2 || cfg.AuthWhitelist[1] != "192.168.1.0/24" {
		t.Errorf("unexpected AuthWhitelist: %v", cfg.AuthWhitelist)
	}
	if cfg.LoginMaxFailures != 5 || cfg.LoginBanDuration != 120*time.Second || cfg.RealIPHeader != "cf-connecting-ip" || !cfg.CookieSecure {
		t.Errorf("unexpected security overrides: %+v", cfg)
	}
	if cfg.FFmpegSessions != 4 || cfg.MediaAuxReserved != 2 || cfg.MediaAuxQueueLimit != 7 {
		t.Errorf("unexpected media overrides: %+v", cfg)
	}
	if cfg.QueueCacheMaxQueues != 12 || cfg.QueueCacheBytes != 2097152 || cfg.QueueIdle != 120*time.Second {
		t.Errorf("unexpected queue overrides: %+v", cfg)
	}
	if cfg.BoltDBPath != "/custom/tags.db" {
		t.Errorf("expected BoltDBPath /custom/tags.db, got %s", cfg.BoltDBPath)
	}
}

func TestInvalidEnvironmentSyntaxFailsValidation(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "MUSIC_PORT", value: "eight-thousand"},
		{key: "MUSIC_COVER_CACHE_BYTES", value: "lots"},
		{key: "MUSIC_COOKIE_SECURE", value: "treu"},
		{key: "MUSIC_RESCAN_INTERVAL", value: "often"},
		{key: "MUSIC_MEDIA_AUX_RESERVED_SESSIONS", value: "one"},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			clearConfigEnv()
			t.Setenv(test.key, test.value)
			cfg := Load()
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected %s=%q to fail validation", test.key, test.value)
			}
			if !strings.Contains(err.Error(), test.key) || !strings.Contains(err.Error(), test.value) {
				t.Fatalf("validation error %q does not identify the invalid setting", err)
			}
		})
	}
}

func TestValidateEmptyDir(t *testing.T) {
	clearConfigEnv()
	os.Setenv("MUSIC_DIR", "")
	defer clearConfigEnv()

	cfg := Load()
	err := cfg.Validate()

	if err == nil {
		t.Error("expected error for empty MusicDir, got nil")
	}
}

func TestValidateInvalidPort(t *testing.T) {
	testCases := []struct {
		name  string
		port  int
		valid bool
	}{
		{"port 0", 0, false},
		{"port -1", -1, false},
		{"port 65536", 65536, false},
		{"port 70000", 70000, false},
		{"port 1", 1, true},
		{"port 65535", 65535, true},
		{"port 8080", 8080, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv()
			os.Setenv("MUSIC_PORT", strconv.Itoa(tc.port))
			cfg := Load()
			err := cfg.Validate()
			if tc.valid && err != nil {
				t.Errorf("expected no error for port %d, got %v", tc.port, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("expected error for port %d, got nil", tc.port)
			}
		})
	}
}

func TestValidateEmptyPassword(t *testing.T) {
	clearConfigEnv()
	os.Setenv("MUSIC_PASSWORD", "")
	defer clearConfigEnv()

	cfg := Load()
	err := cfg.Validate()

	// Empty password should NOT fail validation
	if err != nil {
		t.Errorf("expected no error for empty password, got %v", err)
	}
}

func TestValidateRescanInterval(t *testing.T) {
	cfg := validConfig()
	cfg.RescanInterval = -time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected a negative interval to fail validation")
	}

	for _, interval := range []time.Duration{0, time.Second} {
		cfg = validConfig()
		cfg.RescanInterval = interval
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected interval %v to pass validation: %v", interval, err)
		}
	}
}

func TestExplicitZeroRescanInterval(t *testing.T) {
	clearConfigEnv()
	t.Setenv("MUSIC_RESCAN_INTERVAL", "0")

	cfg := Load()
	if cfg.RescanInterval != 0 {
		t.Fatalf("explicit zero interval = %v, want 0", cfg.RescanInterval)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("explicit zero interval failed validation: %v", err)
	}
}

func TestValidateFFmpegSessions(t *testing.T) {
	cfg := validConfig()
	cfg.FFmpegSessions = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected zero FFmpegSessions to fail validation")
	}
	cfg.FFmpegSessions = 1
	cfg.MediaAuxReserved = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected one FFmpeg session to pass validation: %v", err)
	}
}

func TestSingleMediaSessionCompatibilityAndExplicitReservation(t *testing.T) {
	clearConfigEnv()
	t.Setenv("MUSIC_FFMPEG_MAX_SESSIONS", "1")
	cfg := Load()
	if cfg.MediaAuxReserved != 0 || cfg.MediaAuxReservedSet {
		t.Fatalf("unset reservation with one session = %d/%v", cfg.MediaAuxReserved, cfg.MediaAuxReservedSet)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("legacy one-session config failed: %v", err)
	}
	t.Setenv("MUSIC_MEDIA_AUX_RESERVED_SESSIONS", "1")
	cfg = Load()
	if !cfg.MediaAuxReservedSet || cfg.Validate() == nil {
		t.Fatalf("explicit reservation equal to total was accepted: %+v", cfg)
	}
}

func TestValidateRealIPAndTrustedProxySettings(t *testing.T) {
	cfg := validConfig()
	for _, mode := range []string{"remote", "x-forwarded-for", "cf-connecting-ip"} {
		cfg.RealIPHeader = mode
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid mode %q failed: %v", mode, err)
		}
	}
	cfg.RealIPHeader = "forwarded"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid real IP mode passed validation")
	}
	cfg = validConfig()
	cfg.TrustedProxySubnets = []string{"not-a-subnet"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid trusted proxy passed validation")
	}
}

func validConfig() *Config {
	return &Config{
		Port: 8080, MusicDir: "/music", RescanInterval: time.Minute, OpusBitrate: 160,
		LoginMaxFailures: 3, LoginBanDuration: time.Hour, RealIPHeader: "remote",
		AllowedHosts: []string{"localhost"}, FFmpegSessions: 2, MediaQueueLimit: 8,
		MediaAuxReserved: 1, MediaAuxQueueLimit: 8,
		MediaWaitTimeout: 15 * time.Second, MediaTaskTimeout: 15 * time.Second,
		StreamWriteIdle: time.Minute, MediaNegativeCache: 30 * time.Second,
		MetadataCacheEntries: 4096, CoverCacheEntries: 128, CoverCacheBytes: 64 << 20,
		QueueCacheMaxQueues: 64, QueueCacheBytes: 128 << 20, QueueIdle: 24 * time.Hour,
	}
}

func TestAuthWhitelistPrefixes(t *testing.T) {
	cfg := &Config{AuthWhitelist: []string{"192.168.1.23", "10.0.0.9/24", "::1", "192.168.1.23/32"}}
	prefixes, err := cfg.AuthWhitelistPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		got[i] = prefix.String()
	}
	want := []string{"192.168.1.23/32", "10.0.0.0/24", "::1/128"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("prefixes = %v, want %v", got, want)
	}

	cfg.AuthWhitelist = []string{"not-a-subnet"}
	if _, err := cfg.AuthWhitelistPrefixes(); err == nil {
		t.Fatal("expected invalid whitelist subnet to fail")
	}
}

func clearConfigEnv() {
	os.Unsetenv("MUSIC_PORT")
	os.Unsetenv("MUSIC_DIR")
	os.Unsetenv("MUSIC_RESCAN_INTERVAL")
	os.Unsetenv("MUSIC_OPUS_BITRATE")
	os.Unsetenv("MUSIC_PASSWORD")
	os.Unsetenv("MUSIC_AUTH_WHITELIST_SUBNETS")
	os.Unsetenv("MUSIC_LOGIN_MAX_FAILURES")
	os.Unsetenv("MUSIC_LOGIN_BAN_SECONDS")
	os.Unsetenv("MUSIC_TRUSTED_PROXY_SUBNETS")
	os.Unsetenv("MUSIC_REAL_IP_HEADER")
	os.Unsetenv("MUSIC_COOKIE_SECURE")
	os.Unsetenv("MUSIC_ALLOWED_HOSTS")
	os.Unsetenv("MUSIC_FFMPEG_MAX_SESSIONS")
	os.Unsetenv("MUSIC_MEDIA_AUX_RESERVED_SESSIONS")
	os.Unsetenv("MUSIC_MEDIA_QUEUE_LIMIT")
	os.Unsetenv("MUSIC_MEDIA_AUX_QUEUE_LIMIT")
	os.Unsetenv("MUSIC_MEDIA_WAIT_SECONDS")
	os.Unsetenv("MUSIC_MEDIA_TASK_SECONDS")
	os.Unsetenv("MUSIC_STREAM_WRITE_IDLE_SECONDS")
	os.Unsetenv("MUSIC_MEDIA_NEGATIVE_CACHE_SECONDS")
	os.Unsetenv("MUSIC_METADATA_CACHE_ENTRIES")
	os.Unsetenv("MUSIC_COVER_CACHE_ENTRIES")
	os.Unsetenv("MUSIC_COVER_CACHE_BYTES")
	os.Unsetenv("MUSIC_QUEUE_CACHE_MAX_QUEUES")
	os.Unsetenv("MUSIC_QUEUE_CACHE_BYTES")
	os.Unsetenv("MUSIC_QUEUE_IDLE_SECONDS")
	os.Unsetenv("MUSIC_LEGACY_MUSIC_ROOT")
	os.Unsetenv("MUSIC_BOLTDB_PATH")
}
