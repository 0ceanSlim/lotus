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
	created int
}

func (s *stubShortLinks) GetOrCreate(_ context.Context, pubkey, hash string) (*core.ShortLink, error) {
	s.created++
	return &core.ShortLink{Code: "abc1234", Hash: hash, Pubkey: pubkey}, nil
}

type stubServices struct {
	core.Services
	blobs *stubBlobs
	short *stubShortLinks
}

func (s *stubServices) Blob() core.BlobStorage            { return s.blobs }
func (s *stubServices) ShortLinks() core.ShortLinkService { return s.short }

func testStore(t *testing.T) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	src := "db_path: \"db/x.sqlite3\"\nlog_level: \"INFO\"\napi_addr: \"127.0.0.1:0\"\ncdn_url: \"" + shortTestCdn + "\"\n" +
		"access_control_rules:\n  - action: \"ALLOW\"\n    pubkey: \"ALL\"\n    resource: \"GET\"\nallowed_mime_types:\n  - \"*\"\n"
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

func TestSignedShortLink(t *testing.T) {
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
	r.POST("/api/v1/short/:hash", signedShortLink(svc, testStore(t), zap.NewNop()))

	call := func(hash, auth string) *httptest.ResponseRecorder {
		target := shortTestCdn + "/api/v1/short/" + hash
		req := httptest.NewRequest(http.MethodPost, target, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	target := func(hash string) string { return shortTestCdn + "/api/v1/short/" + hash }

	if w := call(shortTestHash, ""); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Nostr" {
		t.Fatalf("no authorization: %d %v", w.Code, w.Header())
	}
	if w := call(shortTestHash, nip98For(t, stranger, http.MethodPost, target(shortTestHash))); w.Code != http.StatusForbidden {
		t.Fatalf("stranger: %d %s", w.Code, w.Body)
	}
	if w := call(shortTestHash, nip98For(t, owner, http.MethodPost, shortTestCdn+"/upload")); w.Code != http.StatusUnauthorized {
		t.Fatalf("authorization for another URL: %d %s", w.Code, w.Body)
	}
	if w := call(shortTestHash, nip98For(t, owner, http.MethodPut, target(shortTestHash))); w.Code != http.StatusUnauthorized {
		t.Fatalf("authorization for another method: %d %s", w.Code, w.Body)
	}
	if svc.short.created != 0 {
		t.Fatal("a refused request minted a link")
	}
	other := "0000000000000000000000000000000000000000000000000000000000000000"
	if w := call(other, nip98For(t, owner, http.MethodPost, target(other))); w.Code != http.StatusNotFound {
		t.Fatalf("unknown blob: %d %s", w.Code, w.Body)
	}
	if w := call("nope", nip98For(t, owner, http.MethodPost, target("nope"))); w.Code != http.StatusBadRequest {
		t.Fatalf("bad hash: %d %s", w.Code, w.Body)
	}

	w := call(shortTestHash+".png", nip98For(t, owner, http.MethodPost, target(shortTestHash+".png")))
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
	if svc.short.created != 1 {
		t.Fatalf("GetOrCreate called %d times", svc.short.created)
	}
}
