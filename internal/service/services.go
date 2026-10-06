package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

// Services wires the domain services together and implements core.Services.
// Apply pushes a new configuration into every service that holds config
// derived state, so the admin page's changes take effect without a restart.
type Services struct {
	blobs      core.BlobStorage
	acrs       *ACRService
	mimes      *mimeTypeService
	settings   *settingService
	stats      core.StatService
	session    core.SessionService
	short      core.ShortLinkService
	nostrUsers *NostrUsersService
	log        *zap.Logger
}

func New(
	ctx context.Context,
	database *sql.DB,
	queries *db.Queries,
	conf *config.Config,
	log *zap.Logger,
) *Services {
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

	mimeService, err := NewMimeTypeService(ctx, queries, conf, log)
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

	s := &Services{
		blobs:    blobService,
		acrs:     acrService.(*ACRService),
		mimes:    mimeService.(*mimeTypeService),
		settings: settingsService.(*settingService),
		stats:    statService,
		session:  sessionService,
		short:    NewShortLinkService(queries),
		log:      log,
	}

	// The nostr.json fetcher always runs; with an empty URL it just clears
	// the member list, so turning the feature on from the admin page later
	// needs no restart.
	s.nostrUsers = NewNostrUsersService(conf.NostrUsersUrl, log, s.acrs.UpdateNostrUsers)
	return s
}

// Start launches background routines bound to ctx.
func (s *Services) Start(ctx context.Context) {
	s.nostrUsers.StartPeriodicRefresh(ctx, 5*time.Minute)
}

// Apply adopts a new configuration. It returns an error, and changes
// nothing, when the configuration names a MIME type the server does not know.
func (s *Services) Apply(conf config.Config) error {
	if err := s.mimes.Reload(context.Background(), conf.AllowedMimeTypes); err != nil {
		return fmt.Errorf("allowed_mime_types: %w", err)
	}
	s.acrs.Reload(conf.AccessControlRules)
	s.settings.Reload(conf.MaxUploadSizeBytes)
	if q, ok := s.blobs.(interface{ SetQuota(int64) }); ok {
		q.SetQuota(conf.MaxStoragePerPubkeyBytes)
	}
	s.nostrUsers.SetURL(conf.NostrUsersUrl)
	return nil
}

func (s *Services) Blob() core.BlobStorage           { return s.blobs }
func (s *Services) ACR() core.ACRStorage              { return s.acrs }
func (s *Services) Mime() core.MimeTypeService        { return s.mimes }
func (s *Services) Settings() core.SettingService     { return s.settings }
func (s *Services) Stats() core.StatService           { return s.stats }
func (s *Services) Session() core.SessionService      { return s.session }
func (s *Services) ShortLinks() core.ShortLinkService { return s.short }
func (s *Services) NostrUsers() *NostrUsersService    { return s.nostrUsers }
func (s *Services) Init(_ context.Context) error      { return nil }
