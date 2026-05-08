package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/api"
	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/logging"
	"github.com/0ceanSlim/lotus/internal/service"
)

func main() {
	dataDir := resolveDataDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf, err := config.NewConfig(filepath.Join(dataDir, "config.yml"))
	if err != nil {
		log.Fatalf("new config: %v", err)
	}

	logger, err := logging.NewLog(conf.LogLevel)
	if err != nil {
		log.Fatalf("new logger: %v", err)
	}

	dbPath := conf.DbPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(dataDir, dbPath)
	}

	database, err := db.NewDB(dbPath)
	if err != nil {
		logger.Fatal(err.Error())
	}
	queries := db.New(database)

	services := service.New(ctx, database, queries, conf, logger)
	if err := services.Init(ctx); err != nil {
		logger.Error(err.Error())
	}

	if conf.ZeroXZero.Enabled {
		go runExpiryCleanup(ctx, queries, logger)
		logger.Info("started 0x0.st expiry cleanup job")
	}

	if conf.NostrUsersUrl != "" {
		if acrService, ok := services.ACR().(*service.ACRService); ok {
			nostrUsersService := service.NewNostrUsersService(
				conf.NostrUsersUrl,
				logger,
				acrService.UpdateNostrUsers,
			)
			nostrUsersService.StartPeriodicRefresh(ctx, 5*time.Minute)
			logger.Info("started nostr users auto-fetching")
		} else {
			logger.Warn("could not set up nostr users auto-fetching: type assertion failed")
		}
	}

	router := api.SetupRoutes(
		services,
		queries,
		conf.CdnUrl,
		conf.AdminPubkey,
		conf.MaxStoragePerPubkeyBytes,
		logger,
		dataDir,
	)
	router.Run(conf.ApiAddr)
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
