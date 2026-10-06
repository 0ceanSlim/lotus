package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	grainapi "github.com/0ceanslim/grain/client/api"
	"github.com/0ceanslim/grain/client/core/tools"
	"github.com/0ceanslim/grain/client/session"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/nip98"
)

// maxLoginBody caps the login request body we buffer for the NIP-98 payload
// hash. A login is a few hundred bytes of JSON.
const maxLoginBody = 64 << 10

// supportedBuds is advertised on the server info endpoint.
var supportedBuds = []string{"BUD-01", "BUD-02", "BUD-04", "BUD-06", "BUD-08"}

// registerV1Routes mounts grain's client API (session, login, profile,
// media servers, keys, publish), lotus's drive endpoints and the admin API
// under /api/v1. The legacy /api/auth and /api/user surface stays untouched
// for frontends that depend on it.
func registerV1Routes(r *gin.Engine, services core.Services, queries *db.Queries, store *config.Store, members func() []string, log *zap.Logger) {
	v1 := r.Group("/api/v1")

	// Session and sign-in, backed by grain's session manager.
	v1.GET("/session", gin.WrapF(grainapi.GetSessionHandler))
	v1.POST("/auth/login", v1Login(store, log))
	v1.POST("/auth/logout", gin.WrapF(grainapi.LogoutHandler))
	v1.GET("/auth/amber-callback", gin.WrapF(grainapi.HandleAmberCallback))

	// Profile and relay data for the signed-in user.
	v1.GET("/cache", gin.WrapF(grainapi.GetCacheHandler))
	v1.POST("/cache/refresh", gin.WrapF(grainapi.RefreshCacheHandler))
	v1.GET("/user/profile", gin.WrapF(grainapi.GetUserProfileHandler))

	// Blossom server lists (kind 10063): resolve the user's list, build an
	// updated unsigned one, and publish the signed result through grain's
	// outbox routing. The streaming variant reports per-relay results as they
	// arrive. grain's curated server suggestions are deliberately not mounted:
	// the only server lotus suggests is itself, from config.
	v1.GET("/user/media-servers", gin.WrapF(grainapi.GetUserMediaServersHandler))
	v1.POST("/user/media-servers/build", gin.WrapF(grainapi.BuildMediaServersHandler))
	v1.POST("/events/publish", gin.WrapF(grainapi.PublishSignedHandler))
	v1.POST("/events/publish/stream", gin.WrapF(grainapi.PublishSignedStreamHandler))

	// Key utilities. grain's handlers parse the suffix off r.URL.Path.
	v1.Any("/keys/convert/public/*rest", gin.WrapF(grainapi.PublicKeyConversionHandler))
	v1.Any("/keys/decode/nip19/*rest", gin.WrapF(grainapi.Nip19DecodeHandler))

	// Lotus.
	v1.GET("/server/info", serverInfo(store))
	v1.GET("/drive/files", driveFiles(services, store, log))
	v1.POST("/drive/files/:hash/short", driveCreateShortLink(services, store, log))
	v1.DELETE("/drive/short/:code", driveRevokeShortLink(services, log))

	// Admin: reads need the operator's session, writes a NIP-98 signature too.
	v1.GET("/admin/config", adminGetConfig(store))
	v1.POST("/admin/config", adminUpdateConfig(store, log))
	v1.GET("/admin/users", adminUsers(queries, store, members, log))
}

// v1Login binds a write-mode login to proof of key possession: the request
// must carry a NIP-98 authorization signed by the pubkey it claims. grain's
// own login handler trusts the claimed pubkey, which is fine for a relay
// dashboard where every write is a signed event, but a drive session
// authorizes reading and organizing files, so it needs the proof. Read-only
// sessions carry no such authority and may be created unsigned.
func v1Login(store *config.Store, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxLoginBody))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "could not read request body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		var req struct {
			PublicKey     string `json:"public_key"`
			RequestedMode string `json:"requested_mode"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
			return
		}
		mode := req.RequestedMode
		if mode == "" {
			mode = string(session.ReadOnlyMode)
		}

		if mode != string(session.ReadOnlyMode) {
			pubkey, err := nip98.Verify(c.Request, body, store.Get().CdnUrl)
			if err != nil {
				log.Warn("login rejected: NIP-98 verification failed",
					zap.String("claimed_pubkey", req.PublicKey), zap.Error(err))
				c.Header("WWW-Authenticate", "Nostr")
				c.JSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": "login requires a signed NIP-98 authorization: " + err.Error(),
				})
				return
			}
			if !strings.EqualFold(pubkey, req.PublicKey) {
				log.Warn("login rejected: signer does not match claimed pubkey",
					zap.String("claimed_pubkey", req.PublicKey), zap.String("signer", pubkey))
				c.JSON(http.StatusForbidden, gin.H{
					"success": false,
					"message": "the authorization was signed by a different pubkey than the one logging in",
				})
				return
			}
		}

		grainapi.LoginHandler(c.Writer, c.Request)
	}
}

// grainSession returns the grain session for the request, if any.
func grainSession(r *http.Request) *session.UserSession {
	if session.SessionMgr == nil {
		return nil
	}
	return session.SessionMgr.GetCurrentUser(r)
}

// serverInfoResponse describes this deployment. The leading fields match
// grain's MediaServerInfo shape so a client can fetch capability chips for a
// lotus server directly instead of relying on a curated list.
type serverInfoResponse struct {
	Url       string `json:"url"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Cost      string `json:"cost"`
	Retention string `json:"retention"`
	Mirror    bool   `json:"mirror"`
	Note      string `json:"note,omitempty"`
	CTA       string `json:"cta,omitempty"`

	Description      string   `json:"description,omitempty"`
	Icon             string   `json:"icon,omitempty"`
	Contact          string   `json:"contact,omitempty"`
	TermsUrl         string   `json:"terms_url,omitempty"`
	PrivacyUrl       string   `json:"privacy_url,omitempty"`
	AdminPubkey      string   `json:"admin_pubkey,omitempty"`
	AdminNpub        string   `json:"admin_npub,omitempty"`
	AdminProfileUrl  string   `json:"admin_profile_url,omitempty"`
	ProfileUrl       string   `json:"profile_url_template"`
	MaxUploadBytes   int      `json:"max_upload_bytes"`
	QuotaBytes       int64    `json:"quota_bytes"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
	Buds             []string `json:"buds"`
	PublicListing    bool     `json:"public_listing"`
	Claimed          bool     `json:"claimed"`
}

func serverInfo(store *config.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		conf := store.Get()
		cost := conf.Server.Cost
		if cost == "" {
			cost = "free"
		}
		resp := serverInfoResponse{
			Url:              strings.TrimRight(conf.CdnUrl, "/"),
			Kind:             "blossom",
			Name:             conf.DisplayName(),
			Cost:             cost,
			Retention:        conf.Retention(),
			Mirror:           true,
			Note:             conf.Server.Description,
			CTA:              conf.Server.MembershipUrl,
			Description:      conf.Server.Description,
			Icon:             conf.Server.Icon,
			Contact:          conf.Server.Contact,
			TermsUrl:         conf.Server.TermsUrl,
			PrivacyUrl:       conf.Server.PrivacyUrl,
			AdminPubkey:      conf.AdminPubkey,
			MaxUploadBytes:   conf.MaxUploadSizeBytes,
			QuotaBytes:       conf.MaxStoragePerPubkeyBytes,
			AllowedMimeTypes: conf.AllowedMimeTypes,
			Buds:             supportedBuds,
			PublicListing:    conf.PublicListingEnabled(),
			Claimed:          conf.Claimed(),
		}
		resp.ProfileUrl = conf.ProfileURL("{npub}", "{hex}")
		if conf.AdminPubkey != "" {
			if npub, err := tools.EncodePubkey(conf.AdminPubkey); err == nil {
				resp.AdminNpub = npub
				resp.AdminProfileUrl = conf.ProfileURL(npub, conf.AdminPubkey)
			}
		}
		c.Header("Cache-Control", "no-cache")
		c.JSON(http.StatusOK, resp)
	}
}

type driveFile struct {
	Sha256   string `json:"sha256"`
	Url      string `json:"url"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
	Uploaded int64  `json:"uploaded"`
	ShortUrl string `json:"short_url,omitempty"`
	Code     string `json:"short_code,omitempty"`
}

// shortLinkURL is the public form of a short code.
func shortLinkURL(cdnUrl, code string) string {
	return strings.TrimRight(cdnUrl, "/") + "/s/" + code
}

// requireWriteSession returns the session or writes the error response.
func requireWriteSession(c *gin.Context) *session.UserSession {
	sess := grainSession(c.Request)
	if sess == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "sign in first"})
		return nil
	}
	if sess.Mode != session.WriteMode {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "read-only session: sign in with a signer"})
		return nil
	}
	return sess
}

func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// driveFiles lists the signed-in user's blobs, newest first, with the
// storage totals the drive header shows and any short links. Requires a
// write-mode session: a read-only session proves nothing about who is asking.
func driveFiles(services core.Services, store *config.Store, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := requireWriteSession(c)
		if sess == nil {
			return
		}
		conf := store.Get()

		blobs, err := services.Blob().ListMeta(c.Request.Context(), sess.PublicKey)
		if err != nil {
			log.Error("drive: list files failed", zap.String("pubkey", sess.PublicKey), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to list files"})
			return
		}

		shortByHash := map[string]string{}
		if links, err := services.ShortLinks().ListForPubkey(c.Request.Context(), sess.PublicKey); err == nil {
			for _, l := range links {
				shortByHash[l.Hash] = l.Code
			}
		}

		files := make([]driveFile, 0, len(blobs))
		var total int64
		for _, b := range blobs {
			total += b.Size
			f := driveFile{
				Sha256:   b.Sha256,
				Url:      b.Url,
				Size:     b.Size,
				Type:     b.Type,
				Uploaded: b.Uploaded,
			}
			if code, ok := shortByHash[strings.ToLower(b.Sha256)]; ok {
				f.Code = code
				f.ShortUrl = shortLinkURL(conf.CdnUrl, code)
			}
			files = append(files, f)
		}

		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"success":     true,
			"pubkey":      sess.PublicKey,
			"files":       files,
			"count":       len(files),
			"total_bytes": total,
			"quota_bytes": conf.MaxStoragePerPubkeyBytes,
		})
	}
}

// driveCreateShortLink mints (or returns) the session user's short link for a
// blob they own. The canonical sha256 URL is unaffected.
func driveCreateShortLink(services core.Services, store *config.Store, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := requireWriteSession(c)
		if sess == nil {
			return
		}
		hash := strings.ToLower(strings.SplitN(c.Param("hash"), ".", 2)[0])
		if !isHexHash(hash) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid blob hash"})
			return
		}
		meta, err := services.Blob().Meta(c.Request.Context(), hash)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "no such blob on this server"})
			return
		}
		if !strings.EqualFold(meta.Pubkey, sess.PublicKey) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "that blob belongs to a different pubkey"})
			return
		}
		link, err := services.ShortLinks().GetOrCreate(c.Request.Context(), sess.PublicKey, hash)
		if err != nil {
			log.Error("short link create failed", zap.String("hash", hash), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "could not create a short link"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"code":      link.Code,
			"short_url": shortLinkURL(store.Get().CdnUrl, link.Code),
			"url":       meta.Url,
		})
	}
}

func driveRevokeShortLink(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := requireWriteSession(c)
		if sess == nil {
			return
		}
		code := c.Param("code")
		if err := services.ShortLinks().Revoke(c.Request.Context(), sess.PublicKey, code); err != nil {
			if err == core.ErrShortLinkNotFound {
				c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "no such short link"})
				return
			}
			log.Error("short link revoke failed", zap.String("code", code), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "could not revoke the short link"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}
