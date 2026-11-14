package main

import (
	"context"
	"database/sql"
	"flag"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	lotus "github.com/0ceanSlim/lotus"
	"github.com/0ceanSlim/lotus/internal/api"
	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/grainclient"
	"github.com/0ceanSlim/lotus/internal/logging"
	"github.com/0ceanSlim/lotus/internal/ratelimit"
	"github.com/0ceanSlim/lotus/internal/service"
)

func main() {
	dataDir := resolveDataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data directory %s: %v", dataDir, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// First run: no config.yml yet, so the embedded default is written out.
	// The server then starts unclaimed and advertises /setup.
	store, created, err := config.Load(filepath.Join(dataDir, "config.yml"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	conf := store.Get()

	logger, err := logging.NewLog(conf.LogLevel)
	if err != nil {
		log.Fatalf("new logger: %v", err)
	}
	if created {
		logger.Info("wrote default config", zap.String("path", store.Path()))
	}

	dbPath := conf.DbPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(dataDir, dbPath)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		logger.Fatal("create database directory: " + err.Error())
	}

	database, err := db.NewDB(dbPath)
	if err != nil {
		logger.Fatal(err.Error())
	}
	queries := db.New(database)

	services := service.New(ctx, database, queries, &conf, logger)

	// Per-network upload limiter. Its secret lives only in this process, so a
	// restart forgets every address it ever saw.
	limiter, err := ratelimit.New(ratelimit.FromConfig(conf.RateLimit))
	if err != nil {
		logger.Fatal("upload rate limiter: " + err.Error())
	}
	store.SetApplyHook(func(next config.Config) error {
		if err := services.Apply(next); err != nil {
			return err
		}
		return limiter.Reload(ratelimit.FromConfig(next.RateLimit))
	})
	services.Start(ctx)

	if rl := conf.RateLimit; rl.Enabled {
		logger.Info("upload rate limiter enabled; client addresses are kept out of this log",
			zap.Int("uploads_per_minute_per_ip", rl.UploadsPerMinutePerIP),
			zap.Int64("bytes_per_window_per_ip", rl.BytesPerWindowPerIP),
			zap.String("window", rl.WindowDuration().String()),
			zap.Int("max_concurrent_per_ip", rl.MaxConcurrentPerIP),
			zap.Bool("anonymous_uploads", rl.AnonymousUploads),
			zap.Bool("exempt_trusted_pubkeys", rl.ExemptTrustedPubkeys))
	}

	if conf.ZeroXZero.Enabled {
		go runExpiryCleanup(ctx, queries, logger)
		logger.Info("started 0x0.st expiry cleanup job")
	}

	// grain's Nostr client: relay pool, profile resolution, and the session
	// manager behind /api/v1. Index relays connect in the background, so a
	// failure here is a configuration problem rather than a network one.
	if err := grainclient.Init(ctx, conf.IndexRelays); err != nil {
		logger.Fatal("init nostr client: " + err.Error())
	}
	defer grainclient.Shutdown()

	// Frontend: a web/ folder in the data directory wins (bring your own, or
	// the 0x0 frontend); otherwise the drive frontend compiled into the binary.
	var webFS fs.FS
	webDir := filepath.Join(dataDir, "web")
	if info, err := os.Stat(webDir); err == nil && info.IsDir() {
		webFS = os.DirFS(webDir)
		logger.Info("serving frontend from data directory", zap.String("path", webDir))
	} else {
		webFS, err = fs.Sub(lotus.DriveFS, lotus.DriveRoot)
		if err != nil {
			logger.Fatal("embedded frontend: " + err.Error())
		}
		logger.Info("serving embedded drive frontend")
	}

	if !conf.Claimed() {
		logger.Warn("this server has no operator yet: open /setup in a browser to claim it")
	}

	router := api.SetupRoutes(services, queries, store, limiter, services.NostrUsers().GetCachedKeys, logger, webFS)
	logger.Info("listening", zap.String("addr", conf.ApiAddr), zap.String("cdn_url", conf.CdnUrl))
	if err := router.Run(conf.ApiAddr); err != nil {
		logger.Fatal(err.Error())
	}
}

func resolveDataDir() string {
	dataDirFlag := flag.String("data-dir", "", "path to data directory (contains config.yml, web/, db)")
	flag.Parse()

	if *dataDirFlag != "" {
		return *dataDirFlag
	}

	if env := os.Getenv("BLOSSOM_DATA_DIR"); env != "" {
		return env
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("could not determine home directory: %v", err)
	}
	return filepath.Join(home, ".blossom")
}

func runExpiryCleanup(ctx context.Context, queries *db.Queries, logger *zap.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := queries.DeleteExpiredBlobs0x0(ctx, sql.NullInt64{Int64: time.Now().Unix(), Valid: true}); err != nil {
				logger.Error("0x0 expiry cleanup failed", zap.Error(err))
			}
		}
	}
}
