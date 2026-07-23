package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/api"
	"github.com/ColderCoder/ShuffleMuse/internal/auth"
	"github.com/ColderCoder/ShuffleMuse/internal/config"
	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
	"github.com/ColderCoder/ShuffleMuse/internal/stream"
	"github.com/ColderCoder/ShuffleMuse/internal/tags"
	"github.com/ColderCoder/ShuffleMuse/web"
)

var (
	version   = "dev"
	revision  = "unknown"
	buildDate = "unknown"
)

func versionLine() string {
	return fmt.Sprintf("ShuffleMuse %s (commit %s, built %s)", version, revision, buildDate)
}

func versionRequested(args []string) bool {
	return len(args) == 2 && (args[1] == "--version" || args[1] == "-version")
}

type spaFS struct {
	fsys fs.FS
}

func (s spaFS) Open(name string) (fs.File, error) {
	f, err := s.fsys.Open(name)
	if err == nil {
		return f, nil
	}
	if strings.HasPrefix(name, "assets/") || filepath.Ext(name) != "" {
		return nil, err
	}
	return s.fsys.Open("index.html")
}

func staticCacheHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	if versionRequested(os.Args) {
		fmt.Println(versionLine())
		return
	}

	log.Printf("starting %s", versionLine())
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Printf("warning: ffmpeg not found in PATH — transcoding will fail")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		log.Printf("warning: ffprobe not found in PATH — metadata and embedded cover extraction will fail")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.BoltDBPath), 0o755); err != nil {
		log.Fatalf("create data directory: %v", err)
	}

	tagStore, err := tags.Open(cfg.BoltDBPath)
	if err != nil {
		log.Fatalf("open tag store: %v", err)
	}

	whitelist, err := cfg.AuthWhitelistPrefixes()
	if err != nil {
		log.Fatalf("parse authentication whitelist: %v", err)
	}
	if cfg.AuthPassword == "" {
		log.Printf("warning: MUSIC_PASSWORD is empty; authentication is disabled")
		if len(whitelist) > 0 {
			log.Printf("warning: authentication whitelist is ignored while authentication is disabled")
		}
	} else if len(whitelist) > 0 {
		log.Printf("authentication bypass enabled for %d direct-peer subnet(s)", len(whitelist))
	}
	trustedProxies, err := cfg.TrustedProxyPrefixes()
	if err != nil {
		log.Fatalf("parse trusted proxy subnets: %v", err)
	}
	log.Printf("client IP source: %s (%d trusted proxy subnet(s))", cfg.RealIPHeader, len(trustedProxies))
	log.Printf("media process limit: %d total, %d auxiliary reserved", cfg.FFmpegSessions, cfg.MediaAuxReserved)
	mediaManager := mediaexec.NewManager(mediaexec.ManagerConfig{
		MaxSessions: cfg.FFmpegSessions, AuxReserved: cfg.MediaAuxReserved,
		TranscodeWaiters: cfg.MediaQueueLimit, AuxWaiters: cfg.MediaAuxQueueLimit,
		WaitTimeout: cfg.MediaWaitTimeout,
	})
	router := &stream.Router{
		Bitrate: cfg.OpusBitrate, Media: mediaManager,
		StartupTimeout: cfg.MediaTaskTimeout, WriteIdle: cfg.StreamWriteIdle,
	}
	a := auth.New(cfg.AuthPassword, whitelist...)
	a.SetCookieSecure(cfg.CookieSecure)
	apiHandler := api.NewAPI(cfg, nil, tagStore, router, a, mediaManager)

	rescanner := index.NewRescanner(cfg, nil, func(newIdx *index.Index) error {
		knownPaths := make(map[string]bool, len(newIdx.Files))
		for i := range newIdx.Files {
			knownPaths[newIdx.Files[i].Filepath] = true
		}
		migrated, migrateErr := tagStore.MigrateToRelativePaths(knownPaths, cfg.LegacyMusicRoot)
		if migrateErr != nil {
			return migrateErr
		}
		if migrated > 0 {
			log.Printf("migrated %d tag entries to relative paths", migrated)
		}
		return nil
	})
	apiHandler.SetLibraryProvider(rescanner)

	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler.RoutesWithAuth())

	distFS, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		log.Fatalf("create sub filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(spaFS{fsys: distFS}))
	mux.Handle("/", api.SecurityMiddleware(cfg, staticCacheHandler(fileServer)))

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("server listening on port %d", cfg.Port)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	rescanner.Start(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	log.Println("shutting down…")
	rescanner.BeginShutdown()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown error: %v", err)
		if closeErr := srv.Close(); closeErr != nil {
			log.Printf("force-close http connections: %v", closeErr)
		}
	}

	rescanner.Stop()
	if ok := mediaManager.Shutdown(shutdownCtx); !ok {
		log.Println("shutdown deadline reached while waiting for media tasks")
	}

	if err := tagStore.Close(); err != nil {
		log.Printf("tag store close error: %v", err)
	}

	log.Println("server stopped")
}
