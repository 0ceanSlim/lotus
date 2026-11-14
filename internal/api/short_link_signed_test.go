package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	graincore "github.com/0ceanslim/grain/client/core"
	nostr "github.com/0ceanslim/grain/server/types"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/nip98"
	"github.com/0ceanSlim/lotus/internal/ratelimit"
)

const (
	shortTestHash = "6fe9e497859ad682ca2acdc93c891a8baa2fe10489d295497a5952b76242fbed"
	shortTestCdn  = "http://localhost:9999"
)

type stubBlobs struct {
	core.BlobStorage
	owner string
}

func (s *stubBlobs) Meta(_ context.Context, sha string) (*core.Blob, error) {
	if sha != shortTestHash {
		return nil, core.ErrBlobNotFound
	}
	return &core.Blob{Pubkey: s.owner, Sha256: sha, Url: shortTestCdn + "/" + sha}, nil
}

type stubShortLinks struct {
	core.ShortLinkService
	created []string // pubkeys that minted a link
}

func (s *stubShortLinks) GetOrCreate(_ context.Context, pubkey, hash string) (*core.ShortLink, error) {
	s.created = append(s.created, pubkey)
	return &core.ShortLink{Code: "abc1234", Hash: hash, Pubkey: pubkey}, nil
}

type stubServices struct {
	core.Services
	blobs *stubBlobs
	short *stubShortLinks
}

func (s *stubServices) Blob() core.BlobStorage            { return s.blobs }
func (s *stubServices) ShortLinks() core.ShortLinkService { return s.short }

// testStore loads a minimal config; publicListing false mimics a drive
// deployment, true (the default when the key is absent) a 0x0 one.
func testStore(t *testing.T, publicListing bool) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	src := "db_path: \"db/x.sqlite3\"\nlog_level: \"INFO\"\napi_addr: \"127.0.0.1:0\"\ncdn_url: \"" + shortTestCdn + "\"\n" +
		"access_control_rules:\n  - action: \"ALLOW\"\n    pubkey: \"ALL\"\n    resource: \"GET\"\nallowed_mime_types:\n  - \"*\"\n"
	if !publicListing {
		src += "public_listing: false\n"
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func nip98For(t *testing.T, signer *graincore.EventSigner, method, target string) string {
	t.Helper()
	evt := nostr.Event{
		PubKey:    signer.PublicKey(),
		CreatedAt: time.Now().Unix(),
		Kind:      nip98.Kind,
		Tags:      [][]string{{"u", target}, {"method", method}},
	}
	if err := signer.SignEvent(&evt); err != nil {
		t.Fatal(err)
	}
	hdr, err := nip98.Encode(evt)
	if err != nil {
		t.Fatal(err)
	}
	return hdr
}

type shortLinkRig struct {
	r        *gin.Engine
	svc      *stubServices
	owner    *graincore.EventSigner
	stranger *graincore.EventSigner
}

func newShortLinkRig(t *testing.T, publicListing bool, limiter *ratelimit.Limiter) *shortLinkRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	owner, err := graincore.NewEventSignerFromRandom()
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := graincore.NewEventSignerFromRandom()
	if err != nil {
		t.Fatal(err)
	}
	svc := &stubServices{blobs: &stubBlobs{owner: owner.PublicKey()}, short: &stubShortLinks{}}
	r := gin.New()
	r.POST("/api/v1/short/:hash", signedShortLink(svc, testStore(t, publicListing), limiter, nil, zap.NewNop()))
	return &shortLinkRig{r: r, svc: svc, owner: owner, stranger: stranger}
}

func (rig *shortLinkRig) call(hash, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, shortTestCdn+"/api/v1/short/"+hash, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	rig.r.ServeHTTP(w, req)
	return w
}

func shortTarget(hash string) string { return shortTestCdn + "/api/v1/short/" + hash }

func TestSignedShortLinkAuthorization(t *testing.T) {
	rig := newShortLinkRig(t, false, nil)
	if w := rig.call(shortTestHash, ""); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Nostr" {
		t.Fatalf("no authorization: %d %v", w.Code, w.Header())
	}
	if w := rig.call(shortTestHash, nip98For(t, rig.owner, http.MethodPost, shortTestCdn+"/upload")); w.Code != http.StatusUnauthorized {
		t.Fatalf("authorization for another URL: %d %s", w.Code, w.Body)
	}
	if w := rig.call(shortTestHash, nip98For(t, rig.owner, http.MethodPut, shortTarget(shortTestHash))); w.Code != http.StatusUnauthorized {
		t.Fatalf("authorization for another method: %d %s", w.Code, w.Body)
	}
	other := "0000000000000000000000000000000000000000000000000000000000000000"
	if w := rig.call(other, nip98For(t, rig.owner, http.MethodPost, shortTarget(other))); w.Code != http.StatusNotFound {
		t.Fatalf("unknown blob: %d %s", w.Code, w.Body)
	}
	if w := rig.call("nope", nip98For(t, rig.owner, http.MethodPost, shortTarget("nope"))); w.Code != http.StatusBadRequest {
		t.Fatalf("bad hash: %d %s", w.Code, w.Body)
	}
	if len(rig.svc.short.created) != 0 {
		t.Fatal("a refused request minted a link")
	}

	w := rig.call(shortTestHash+".png", nip98For(t, rig.owner, http.MethodPost, shortTarget(shortTestHash+".png")))
	if w.Code != http.StatusOK {
		t.Fatalf("owner: %d %s", w.Code, w.Body)
	}
	var body struct {
		Success  bool   `json:"success"`
		Code     string `json:"code"`
		ShortUrl string `json:"short_url"`
		Url      string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Code != "abc1234" || body.ShortUrl != shortTestCdn+"/s/abc1234" || body.Url != shortTestCdn+"/"+shortTestHash {
		t.Fatalf("response %+v", body)
	}
	if len(rig.svc.short.created) != 1 || rig.svc.short.created[0] != rig.owner.PublicKey() {
		t.Fatalf("GetOrCreate calls: %v", rig.svc.short.created)
	}
}

func TestSignedShortLinkOwnershipFollowsPublicListing(t *testing.T) {
	// Private blobs (drive): only the owner.
	private := newShortLinkRig(t, false, nil)
	if w := private.call(shortTestHash, nip98For(t, private.stranger, http.MethodPost, shortTarget(shortTestHash))); w.Code != http.StatusForbidden {
		t.Fatalf("stranger on a private server: %d %s", w.Code, w.Body)
	}
	if len(private.svc.short.created) != 0 {
		t.Fatal("stranger minted a link on a private server")
	}

	// Public listing (0x0): anyone with a key, under their own pubkey, so the
	// visitor who re-uploaded bytes someone else owns still gets a link.
	public := newShortLinkRig(t, true, nil)
	w := public.call(shortTestHash, nip98For(t, public.stranger, http.MethodPost, shortTarget(shortTestHash)))
	if w.Code != http.StatusOK {
		t.Fatalf("stranger on a public server: %d %s", w.Code, w.Body)
	}
	if got := public.svc.short.created; len(got) != 1 || got[0] != public.stranger.PublicKey() {
		t.Fatalf("link minted under %v, want the stranger's pubkey", got)
	}
}

func TestSignedShortLinkIsRateLimited(t *testing.T) {
	limiter, err := ratelimit.New(ratelimit.Config{Enabled: true, UploadsPerMinute: 1})
	if err != nil {
		t.Fatal(err)
	}
	rig := newShortLinkRig(t, true, limiter)
	auth := nip98For(t, rig.owner, http.MethodPost, shortTarget(shortTestHash))
	if w := rig.call(shortTestHash, auth); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	w := rig.call(shortTestHash, auth)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || w.Header().Get(HeaderXReason) == "" {
		t.Fatalf("second should be refused like an upload: %d %v", w.Code, w.Header())
	}
	if len(rig.svc.short.created) != 1 {
		t.Fatal("a refused request minted a link")
	}
	// An unsigned request is refused before it costs anything.
	if w := rig.call(shortTestHash, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", w.Code)
	}
}
