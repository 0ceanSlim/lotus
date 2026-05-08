package service

import (
	"context"
	"database/sql"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

type services struct {
	blobs    core.BlobStorage
	acrs     core.ACRStorage
	mimes    core.MimeTypeService
	settings core.SettingService
	stats    core.StatService
	session  core.SessionService
	conf     *config.Config
}

func New(
	ctx context.Context,
	database *sql.DB,
	queries *db.Queries,
	conf *config.Config,
	log *zap.Logger,
) core.Services {
	var blobService core.BlobStorage
	var err error
	if conf.ZeroXZero.Enabled {
		blobService, err = NewZeroXZeroBlobService(database, queries, conf, log)
	} else {
		blobService, err = NewBlobService(database, queries, conf.CdnUrl, conf.MaxStoragePerPubkeyBytes, log)
	}
	if err != nil {
		log.Fatal(err.Error())
	}

	acrService, err := NewACRService(conf, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	settingsService, err := NewSettingService(conf.MaxUploadSizeBytes)
	if err != nil {
		log.Fatal(err.Error())
	}

	mimeTypeService, err := NewMimeTypeService(ctx, queries, conf, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	statService, err := NewStatService(queries)
	if err != nil {
		log.Fatal(err.Error())
	}

	sessionService, err := NewSessionService(database, queries, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	return &services{
		blobs:    blobService,
		acrs:     acrService,
		mimes:    mimeTypeService,
		settings: settingsService,
		stats:    statService,
		session:  sessionService,
		conf:     conf,
	}
}

func (s *services) Blob() core.BlobStorage    { return s.blobs }
func (s *services) ACR() core.ACRStorage       { return s.acrs }
func (s *services) Mime() core.MimeTypeService { return s.mimes }
func (s *services) Settings() core.SettingService { return s.settings }
func (s *services) Stats() core.StatService    { return s.stats }
func (s *services) Session() core.SessionService { return s.session }
func (s *services) Init(_ context.Context) error { return nil }
