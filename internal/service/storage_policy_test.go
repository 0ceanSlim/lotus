package service

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func storageTestDB(t *testing.T) *db.Queries {
	t.Helper()
	database, err := db.NewDB(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return db.New(database)
}

func TestEnsureSpaceRefusesThenEvictsByAgeTimesSize(t *testing.T) {
	ctx := context.Background()
	queries := storageTestDB(t)
	svc, err := NewBlobService(nil, queries, "http://cdn", 0, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	bs := svc.(*blobService)

	now := time.Now().Unix()
	insert := func(hash string, size, age int64) {
		t.Helper()
		if _, err := queries.InsertBlob(ctx, db.InsertBlobParams{
			Pubkey: "p", Hash: hash, Type: "application/octet-stream", Size: size, Blob: make([]byte, size), Created: now - age,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Scores (age × size, age floored at an hour): A 500 × 7200 = 3.6M,
	// B 100 × 72000 = 7.2M, C 300 × 3600 (ten seconds old, floored) = 1.08M.
	// B goes first, then A; C, the freshest and not the biggest, last.
	insert(hashA, 500, 7200)
	insert(hashB, 100, 72000)
	insert(hashC, 300, 10)
	if err := queries.InsertShortLink(ctx, db.InsertShortLinkParams{Code: "bbbbbbb", Hash: hashB, Pubkey: "p", Created: now}); err != nil {
		t.Fatal(err)
	}

	// No cap: anything fits.
	if err := bs.EnsureSpace(ctx, 1<<40); err != nil {
		t.Fatalf("uncapped: %v", err)
	}

	// Refuse mode: fits or 507, nothing deleted.
	bs.SetStoragePolicy(core.StoragePolicy{MaxTotalBytes: 1000})
	if err := bs.EnsureSpace(ctx, 100); err != nil {
		t.Fatalf("900+100 should fit under 1000: %v", err)
	}
	if err := bs.EnsureSpace(ctx, 101); !errors.Is(err, core.ErrServerFull) {
		t.Fatalf("900+101 over 1000 should refuse, got %v", err)
	}
	if err := bs.EnsureSpace(ctx, 2000); !errors.Is(err, core.ErrServerFull) {
		t.Fatalf("bigger than the cap should refuse, got %v", err)
	}
	if total, _ := queries.GetTotalBlobBytes(ctx); total != 900 {
		t.Fatalf("refuse mode deleted something: total %d", total)
	}

	// Evict mode: need 300 → B (100) then A (500); C survives.
	bs.SetStoragePolicy(core.StoragePolicy{MaxTotalBytes: 1000, Evict: true})
	if err := bs.EnsureSpace(ctx, 400); err != nil {
		t.Fatalf("evict: %v", err)
	}
	if _, err := queries.GetBlobFromHash(ctx, hashB); err == nil {
		t.Fatal("B (oldest × size) should have been evicted first")
	}
	if _, err := queries.GetBlobFromHash(ctx, hashA); err == nil {
		t.Fatal("A should have been evicted second")
	}
	if _, err := queries.GetBlobFromHash(ctx, hashC); err != nil {
		t.Fatal("C, the freshest, should survive")
	}
	if _, err := queries.GetShortLink(ctx, "bbbbbbb"); err == nil {
		t.Fatal("short link to an evicted blob survived")
	}
	if total, _ := queries.GetTotalBlobBytes(ctx); total != 300 {
		t.Fatalf("total after eviction %d, want 300", total)
	}

	// Still refuses a single file bigger than the whole cap.
	if err := bs.EnsureSpace(ctx, 2000); !errors.Is(err, core.ErrServerFull) {
		t.Fatalf("oversize with evict: %v", err)
	}
	// Evicts everything when it must.
	bs.SetStoragePolicy(core.StoragePolicy{MaxTotalBytes: 350, Evict: true})
	if err := bs.EnsureSpace(ctx, 100); err != nil {
		t.Fatalf("evict last blob: %v", err)
	}
	if total, _ := queries.GetTotalBlobBytes(ctx); total != 0 {
		t.Fatalf("total %d, want 0", total)
	}
}

func TestZeroXZeroQuotaAndEviction(t *testing.T) {
	ctx := context.Background()
	queries := storageTestDB(t)
	conf := &config.Config{
		CdnUrl:                   "http://cdn",
		MaxStoragePerPubkeyBytes: 150,
		ZeroXZero:                config.ZeroXZeroConfig{Enabled: true, InstanceUrl: "http://127.0.0.1:1"},
	}
	svc, err := NewZeroXZeroBlobService(nil, queries, conf, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	zs := svc.(*zeroXZeroBlobService)

	now := time.Now().Unix()
	insert := func(hash, pubkey string, size, age int64) {
		t.Helper()
		if _, err := queries.InsertBlob0x0(ctx, db.InsertBlob0x0Params{
			Hash: hash, Pubkey: pubkey, Url: "http://127.0.0.1:1/" + hash, Size: size, Type: "text/plain",
			Expiry: sql.NullInt64{Int64: now + 3600, Valid: true}, Created: now - age,
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert(hashA, "p", 100, 100)

	// The per-pubkey quota is real on the 0x0 backend now.
	if err := zs.ValidateStorageQuota(ctx, "p", 50); err != nil {
		t.Fatalf("100+50 within 150: %v", err)
	}
	if err := zs.ValidateStorageQuota(ctx, "p", 51); !errors.Is(err, core.ErrStorageQuotaExceeded) {
		t.Fatalf("100+51 over 150: %v", err)
	}
	if err := zs.ValidateStorageQuota(ctx, "q", 150); err != nil {
		t.Fatalf("another pubkey has its own quota: %v", err)
	}
	zs.SetQuota(0)
	if err := zs.ValidateStorageQuota(ctx, "p", 1<<30); err != nil {
		t.Fatalf("quota off: %v", err)
	}

	// Eviction works over the 0x0 table too (the backend delete is best
	// effort; here it is a refused connection). Need 90: A (100 × 3600
	// floored = 360k) outranks B (40 × 5000 = 200k) and alone frees enough.
	insert(hashB, "q", 40, 5000)
	zs.SetStoragePolicy(core.StoragePolicy{MaxTotalBytes: 150, Evict: true})
	if err := zs.EnsureSpace(ctx, 100); err != nil {
		t.Fatalf("evict on 0x0: %v", err)
	}
	if _, err := queries.GetBlob0x0FromHash(ctx, hashA); err == nil {
		t.Fatal("A should have been evicted")
	}
	if _, err := queries.GetBlob0x0FromHash(ctx, hashB); err != nil {
		t.Fatal("B should survive: A alone made room")
	}
	if err := queries.DeleteBlob0x0FromHash(ctx, hashB); err != nil {
		t.Fatal(err)
	}
	zs.SetStoragePolicy(core.StoragePolicy{MaxTotalBytes: 150})
	insert(hashC, "p", 150, 1)
	if err := zs.EnsureSpace(ctx, 1); !errors.Is(err, core.ErrServerFull) {
		t.Fatalf("refuse mode on 0x0: %v", err)
	}
}
