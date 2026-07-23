package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                 int
	MusicDir             string
	RescanInterval       time.Duration
	OpusBitrate          int
	AuthPassword         string
	AuthWhitelist        []string
	LoginMaxFailures     int
	LoginBanDuration     time.Duration
	TrustedProxySubnets  []string
	RealIPHeader         string
	CookieSecure         bool
	AllowedHosts         []string
	FFmpegSessions       int
	MediaAuxReserved     int
	MediaAuxReservedSet  bool
	MediaQueueLimit      int
	MediaAuxQueueLimit   int
	MediaWaitTimeout     time.Duration
	MediaTaskTimeout     time.Duration
	StreamWriteIdle      time.Duration
	MediaNegativeCache   time.Duration
	MetadataCacheEntries int
	CoverCacheEntries    int
	CoverCacheBytes      int64
	QueueCacheMaxQueues  int
	QueueCacheBytes      int64
	QueueIdle            time.Duration
	LegacyMusicRoot      string
	BoltDBPath           string
	parseErr             error
}

func Load() *Config {
	loader := &envLoader{}
	ffmpegSessions := loader.int("MUSIC_FFMPEG_MAX_SESSIONS", 2)
	auxReserved, auxReservedSet := loader.auxReserved(ffmpegSessions)
	cfg := &Config{
		Port:                 loader.int("MUSIC_PORT", 8080),
		MusicDir:             getMusicDir(),
		RescanInterval:       loader.duration("MUSIC_RESCAN_INTERVAL", 0),
		OpusBitrate:          loader.int("MUSIC_OPUS_BITRATE", 160),
		AuthPassword:         getEnv("MUSIC_PASSWORD", ""),
		AuthWhitelist:        getEnvList("MUSIC_AUTH_WHITELIST_SUBNETS"),
		LoginMaxFailures:     loader.int("MUSIC_LOGIN_MAX_FAILURES", 3),
		LoginBanDuration:     time.Duration(loader.int("MUSIC_LOGIN_BAN_SECONDS", 3600)) * time.Second,
		TrustedProxySubnets:  getEnvList("MUSIC_TRUSTED_PROXY_SUBNETS"),
		RealIPHeader:         strings.ToLower(getEnv("MUSIC_REAL_IP_HEADER", "remote")),
		CookieSecure:         loader.bool("MUSIC_COOKIE_SECURE", false),
		AllowedHosts:         getEnvListDefault("MUSIC_ALLOWED_HOSTS", []string{"localhost", "127.0.0.1", "::1"}),
		FFmpegSessions:       ffmpegSessions,
		MediaAuxReserved:     auxReserved,
		MediaAuxReservedSet:  auxReservedSet,
		MediaQueueLimit:      loader.int("MUSIC_MEDIA_QUEUE_LIMIT", 8),
		MediaAuxQueueLimit:   loader.int("MUSIC_MEDIA_AUX_QUEUE_LIMIT", 8),
		MediaWaitTimeout:     time.Duration(loader.int("MUSIC_MEDIA_WAIT_SECONDS", 15)) * time.Second,
		MediaTaskTimeout:     time.Duration(loader.int("MUSIC_MEDIA_TASK_SECONDS", 15)) * time.Second,
		StreamWriteIdle:      time.Duration(loader.int("MUSIC_STREAM_WRITE_IDLE_SECONDS", 60)) * time.Second,
		MediaNegativeCache:   time.Duration(loader.int("MUSIC_MEDIA_NEGATIVE_CACHE_SECONDS", 30)) * time.Second,
		MetadataCacheEntries: loader.int("MUSIC_METADATA_CACHE_ENTRIES", 4096),
		CoverCacheEntries:    loader.int("MUSIC_COVER_CACHE_ENTRIES", 128),
		CoverCacheBytes:      loader.int64("MUSIC_COVER_CACHE_BYTES", 64<<20),
		QueueCacheMaxQueues:  loader.int("MUSIC_QUEUE_CACHE_MAX_QUEUES", 64),
		QueueCacheBytes:      loader.int64("MUSIC_QUEUE_CACHE_BYTES", 128<<20),
		QueueIdle:            time.Duration(loader.int("MUSIC_QUEUE_IDLE_SECONDS", 86400)) * time.Second,
		LegacyMusicRoot:      getEnv("MUSIC_LEGACY_MUSIC_ROOT", ""),
		BoltDBPath:           getEnv("MUSIC_BOLTDB_PATH", "./data/tags.db"),
	}
	cfg.parseErr = loader.err
	return cfg
}

func (c *Config) Validate() error {
	if c.parseErr != nil {
		return c.parseErr
	}
	if c.Port < 1 || c.Port > 65535 {
		return &ValidationError{Field: "Port", Message: "must be between 1 and 65535"}
	}
	if c.MusicDir == "" {
		return &ValidationError{Field: "MusicDir", Message: "cannot be empty"}
	}
	if c.OpusBitrate <= 0 {
		return &ValidationError{Field: "OpusBitrate", Message: "must be greater than 0"}
	}
	if c.RescanInterval < 0 {
		return &ValidationError{Field: "RescanInterval", Message: "must not be negative"}
	}
	if c.FFmpegSessions <= 0 {
		return &ValidationError{Field: "FFmpegSessions", Message: "must be greater than 0"}
	}
	if c.MediaAuxReserved < 0 || c.MediaAuxReserved >= c.FFmpegSessions {
		return &ValidationError{Field: "MediaAuxReserved", Message: "must be non-negative and less than FFmpegSessions"}
	}
	if c.LoginMaxFailures <= 0 {
		return &ValidationError{Field: "LoginMaxFailures", Message: "must be greater than 0"}
	}
	if c.LoginBanDuration <= 0 {
		return &ValidationError{Field: "LoginBanDuration", Message: "must be greater than 0"}
	}
	switch c.RealIPHeader {
	case "remote", "x-forwarded-for", "cf-connecting-ip":
	default:
		return &ValidationError{Field: "RealIPHeader", Message: "must be remote, x-forwarded-for, or cf-connecting-ip"}
	}
	if c.MediaQueueLimit < 0 {
		return &ValidationError{Field: "MediaQueueLimit", Message: "must not be negative"}
	}
	if c.MediaAuxQueueLimit < 0 {
		return &ValidationError{Field: "MediaAuxQueueLimit", Message: "must not be negative"}
	}
	if c.MediaWaitTimeout <= 0 {
		return &ValidationError{Field: "MediaWaitTimeout", Message: "must be greater than 0"}
	}
	if c.MediaTaskTimeout <= 0 {
		return &ValidationError{Field: "MediaTaskTimeout", Message: "must be greater than 0"}
	}
	if c.StreamWriteIdle <= 0 {
		return &ValidationError{Field: "StreamWriteIdle", Message: "must be greater than 0"}
	}
	if c.MediaNegativeCache <= 0 {
		return &ValidationError{Field: "MediaNegativeCache", Message: "must be greater than 0"}
	}
	if c.MetadataCacheEntries <= 0 {
		return &ValidationError{Field: "MetadataCacheEntries", Message: "must be greater than 0"}
	}
	if c.CoverCacheEntries <= 0 {
		return &ValidationError{Field: "CoverCacheEntries", Message: "must be greater than 0"}
	}
	if c.CoverCacheBytes <= 0 {
		return &ValidationError{Field: "CoverCacheBytes", Message: "must be greater than 0"}
	}
	if c.QueueCacheMaxQueues <= 0 {
		return &ValidationError{Field: "QueueCacheMaxQueues", Message: "must be greater than 0"}
	}
	if c.QueueCacheBytes <= 0 {
		return &ValidationError{Field: "QueueCacheBytes", Message: "must be greater than 0"}
	}
	if c.QueueIdle <= 0 {
		return &ValidationError{Field: "QueueIdle", Message: "must be greater than 0"}
	}
	if c.LegacyMusicRoot != "" && !filepath.IsAbs(c.LegacyMusicRoot) {
		return &ValidationError{Field: "LegacyMusicRoot", Message: "must be an absolute path"}
	}
	if len(c.AllowedHosts) == 0 {
		return &ValidationError{Field: "AllowedHosts", Message: "must contain at least one host"}
	}
	if _, err := c.AuthWhitelistPrefixes(); err != nil {
		return &ValidationError{Field: "AuthWhitelist", Message: err.Error()}
	}
	if _, err := c.TrustedProxyPrefixes(); err != nil {
		return &ValidationError{Field: "TrustedProxySubnets", Message: err.Error()}
	}
	return nil
}

func (c *Config) AuthWhitelistPrefixes() ([]netip.Prefix, error) {
	return parsePrefixes(c.AuthWhitelist)
}

func (c *Config) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	return parsePrefixes(c.TrustedProxySubnets)
}

func parsePrefixes(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]struct{}, len(values))
	for _, value := range values {
		prefix, err := parseIPOrPrefix(value)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid IP address or CIDR", value)
		}
		prefix = prefix.Masked()
		if _, exists := seen[prefix]; exists {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseIPOrPrefix(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		if prefix.Addr().Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("scoped addresses are not supported")
		}
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			return netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96), nil
		}
		return prefix, nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("invalid address")
	}
	address = address.Unmap()
	return netip.PrefixFrom(address, address.BitLen()), nil
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Message
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	values := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func getEnvListDefault(key string, fallback []string) []string {
	if values := getEnvList(key); len(values) > 0 {
		return values
	}
	return append([]string(nil), fallback...)
}

func getMusicDir() string {
	if val, exists := os.LookupEnv("MUSIC_DIR"); exists {
		return val
	}
	return "/music"
}

type envLoader struct {
	err error
}

func (l *envLoader) record(key, value, expected string, err error) {
	if l.err == nil {
		l.err = fmt.Errorf("%s=%q must be %s: %w", key, value, expected, err)
	}
}

func (l *envLoader) int(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		} else {
			l.record(key, val, "an integer", err)
		}
	}
	return defaultVal
}

func (l *envLoader) int64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.ParseInt(val, 10, 64); err == nil {
			return intVal
		} else {
			l.record(key, val, "an integer", err)
		}
	}
	return defaultVal
}

func (l *envLoader) auxReserved(total int) (int, bool) {
	raw, exists := os.LookupEnv("MUSIC_MEDIA_AUX_RESERVED_SESSIONS")
	if !exists || raw == "" {
		if total == 1 {
			return 0, false
		}
		return 1, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		l.record("MUSIC_MEDIA_AUX_RESERVED_SESSIONS", raw, "an integer", err)
		return 1, true
	}
	return value, true
}

func (l *envLoader) bool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		} else {
			l.record(key, val, "a boolean", err)
		}
	}
	return defaultVal
}

func (l *envLoader) duration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if durVal, err := time.ParseDuration(val); err == nil {
			return durVal
		} else {
			l.record(key, val, "a duration", err)
		}
	}
	return defaultVal
}
