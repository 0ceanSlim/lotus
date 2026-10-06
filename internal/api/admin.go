package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/0ceanslim/grain/client/core/tools"
	"github.com/0ceanslim/grain/client/session"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/nip98"
)

// Admin surface. The /admin page and the read endpoints are gated by the
// session: it must belong to the operator. Every mutation additionally
// carries a NIP-98 authorization signed by the operator's key, so a stolen
// cookie can read the dashboard but cannot change anything.

// restartRequiredKeys are config keys the running process cannot adopt.
var restartRequiredKeys = []string{"db_path", "log_level", "api_addr", "cdn_url"}

const maxAdminBody = 256 << 10

// requireAdminSession returns the operator's session or writes the error.
func requireAdminSession(c *gin.Context, store *config.Store) *session.UserSession {
	conf := store.Get()
	sess := grainSession(c.Request)
	if sess == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "sign in first"})
		return nil
	}
	if !conf.Claimed() || !conf.IsAdmin(sess.PublicKey) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "operator only"})
		return nil
	}
	return sess
}

// adminGetConfig returns the running configuration for the admin forms.
func adminGetConfig(store *config.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if requireAdminSession(c, store) == nil {
			return
		}
		conf := store.Get()
		adminNpub := ""
		if npub, err := tools.EncodePubkey(conf.AdminPubkey); err == nil {
			adminNpub = npub
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"success":          true,
			"config":           conf,
			"admin_npub":       adminNpub,
			"config_path":      store.Path(),
			"restart_required": restartRequiredKeys,
		})
	}
}

// adminConfigPatch carries the editable keys; nil fields are left alone.
type adminConfigPatch struct {
	Server                   *config.ServerInfo          `json:"server"`
	PublicListing            *bool                       `json:"public_listing"`
	MaxUploadSizeBytes       *int                        `json:"max_upload_size_bytes"`
	MaxStoragePerPubkeyBytes *int64                      `json:"max_storage_per_pubkey_bytes"`
	NostrUsersUrl            *string                     `json:"nostr_users_url"`
	AccessControlRules       *[]config.AccessControlRule `json:"access_control_rules"`
	AllowedMimeTypes         *[]string                   `json:"allowed_mime_types"`
	AdminPubkey              *string                     `json:"admin_pubkey"`
}

// adminUpdateConfig applies a patch. It accepts npubs wherever a pubkey is
// expected and stores the hex form.
func adminUpdateConfig(store *config.Store, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := requireAdminSession(c, store)
		if sess == nil {
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxAdminBody))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "could not read request body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		conf := store.Get()
		signer, err := nip98.Verify(c.Request, body, conf.CdnUrl)
		if err != nil {
			c.Header("WWW-Authenticate", "Nostr")
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "this change needs a signed NIP-98 authorization: " + err.Error()})
			return
		}
		if !conf.IsAdmin(signer) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "the authorization was not signed by the operator"})
			return
		}

		var patch adminConfigPatch
		if err := json.Unmarshal(body, &patch); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid JSON: " + err.Error()})
			return
		}

		err = store.Update(func(cfg *config.Config) error {
			if patch.Server != nil {
				cfg.Server = *patch.Server
			}
			if patch.PublicListing != nil {
				v := *patch.PublicListing
				cfg.PublicListing = &v
			}
			if patch.MaxUploadSizeBytes != nil {
				cfg.MaxUploadSizeBytes = *patch.MaxUploadSizeBytes
			}
			if patch.MaxStoragePerPubkeyBytes != nil {
				cfg.MaxStoragePerPubkeyBytes = *patch.MaxStoragePerPubkeyBytes
			}
			if patch.NostrUsersUrl != nil {
				cfg.NostrUsersUrl = strings.TrimSpace(*patch.NostrUsersUrl)
			}
			if patch.AccessControlRules != nil {
				rules := make([]config.AccessControlRule, 0, len(*patch.AccessControlRules))
				for _, r := range *patch.AccessControlRules {
					pk, err := toHexPubkey(r.Pubkey)
					if err != nil {
						return err
					}
					rules = append(rules, config.AccessControlRule{Action: r.Action, Pubkey: pk, Resource: r.Resource})
				}
				cfg.AccessControlRules = rules
			}
			if patch.AllowedMimeTypes != nil {
				types := make([]string, 0, len(*patch.AllowedMimeTypes))
				for _, t := range *patch.AllowedMimeTypes {
					if t = strings.TrimSpace(t); t != "" {
						types = append(types, t)
					}
				}
				cfg.AllowedMimeTypes = types
			}
			if patch.AdminPubkey != nil {
				pk, err := toHexPubkey(*patch.AdminPubkey)
				if err != nil {
					return err
				}
				cfg.AdminPubkey = pk
			}
			return nil
		})
		if err != nil {
			log.Warn("admin config update rejected", zap.String("by", signer), zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
		log.Info("admin config updated", zap.String("by", signer))
		c.JSON(http.StatusOK, gin.H{"success": true, "config": store.Get()})
	}
}

// toHexPubkey accepts ALL, a hex pubkey or an npub.
func toHexPubkey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "ALL") {
		return "ALL", nil
	}
	if strings.HasPrefix(strings.ToLower(s), "npub1") {
		return tools.DecodeNpub(s)
	}
	return strings.ToLower(s), nil
}

type adminUser struct {
	Pubkey     string `json:"pubkey"`
	Npub       string `json:"npub"`
	Files      int64  `json:"files"`
	Bytes      int64  `json:"bytes"`
	LastUpload int64  `json:"last_upload"`
	Member     bool   `json:"member"` // listed in the nostr.json
	CanUpload  bool   `json:"can_upload"`
}

// adminUsers lists uploaders by storage used.
func adminUsers(queries *db.Queries, store *config.Store, members func() []string, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if requireAdminSession(c, store) == nil {
			return
		}
		rows, err := queries.ListPubkeyUsage(c.Request.Context())
		if err != nil {
			log.Error("admin users query failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "could not list users"})
			return
		}
		conf := store.Get()
		memberSet := map[string]bool{}
		if members != nil {
			for _, m := range members() {
				memberSet[strings.ToLower(m)] = true
			}
		}
		allowAll := false
		allowed := map[string]bool{}
		for _, r := range conf.AccessControlRules {
			if !strings.EqualFold(r.Resource, "UPLOAD") {
				continue
			}
			if r.Pubkey == "ALL" {
				allowAll = strings.EqualFold(r.Action, "ALLOW")
			} else {
				allowed[strings.ToLower(r.Pubkey)] = strings.EqualFold(r.Action, "ALLOW")
			}
		}
		users := make([]adminUser, 0, len(rows))
		for _, r := range rows {
			pk := strings.ToLower(r.Pubkey)
			u := adminUser{Pubkey: pk, Files: r.Files, Bytes: r.Bytes, Member: memberSet[pk]}
			if r.LastUpload.Valid {
				u.LastUpload = r.LastUpload.Int64
			}
			if npub, err := tools.EncodePubkey(pk); err == nil {
				u.Npub = npub
			}
			if v, ok := allowed[pk]; ok {
				u.CanUpload = v
			} else {
				u.CanUpload = allowAll || u.Member
			}
			users = append(users, u)
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"success": true, "users": users, "quota_bytes": conf.MaxStoragePerPubkeyBytes, "members": len(memberSet)})
	}
}

// setupClaim handles POST /setup: the signed-in user becomes the operator if
// nobody has yet. The session is already bound to a NIP-98 proof, so the
// pubkey is trusted.
func setupClaim(store *config.Store, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Pubkey string `json:"pubkey"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
			return
		}
		sess := grainSession(c.Request)
		if sess == nil || sess.Mode != session.WriteMode {
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "sign in with a signer first"})
			return
		}
		if !strings.EqualFold(sess.PublicKey, req.Pubkey) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "the claim must come from the signed-in key"})
			return
		}
		if err := store.Claim(sess.PublicKey); err != nil {
			if err == config.ErrAlreadyClaimed {
				owner := store.Get().AdminPubkey
				npub, _ := tools.EncodePubkey(owner)
				c.JSON(http.StatusConflict, gin.H{"success": false, "message": "already claimed", "owner_hex": owner, "owner_npub": npub})
				return
			}
			log.Error("claim failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "claim failed: " + err.Error()})
			return
		}
		log.Info("server claimed", zap.String("operator", sess.PublicKey), zap.String("ip", c.ClientIP()))
		c.JSON(http.StatusOK, gin.H{"success": true, "redirect": "/admin"})
	}
}
