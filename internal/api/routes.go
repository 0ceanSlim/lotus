package api

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/0ceanslim/grain/client/core/tools"
	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

// SetupRoutes builds the router. webFS is the frontend: the data directory's
// web/ folder when present, otherwise the embedded drive frontend. members
// returns the pubkeys currently granted upload access by the nostr.json.
func SetupRoutes(
	services core.Services,
	queries *db.Queries,
	store *config.Store,
	members func() []string,
	log *zap.Logger,
	webFS fs.FS,
) *gin.Engine {
	// cdn_url is a restart-required setting, so capturing it once is exact.
	cdnBaseUrl := store.Get().CdnUrl
	r := gin.New()

	r.Use(ginzap.Ginzap(log, time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(log, true))

	r.Use(cors.New(cors.Config{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "PUT", "HEAD", "DELETE"},
		AllowHeaders: []string{
			HeaderAuthorization,
			HeaderContentType,
			HeaderXSHA256,
			HeaderXContentType,
			HeaderXContentLength,
			"Range",
		},
		ExposeHeaders: []string{
			"Content-Length",
			"Accept-Ranges",
			"Content-Range",
			"Content-Type",
		},
	}))

	r.GET("/.well-known/health", func(ctx *gin.Context) {
		ctx.Status(http.StatusOK)
	})

	// Legacy session API. Frontends built before the grain integration
	// (the 0x0 deployment) depend on this exact surface; it stays as is.
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/login", login(services, log))
		authGroup.GET("/session", checkSession(services, log))
		authGroup.POST("/logout", logout(services, log))
		authGroup.POST("/generate-keys", generateKeys(services, log))
		authGroup.GET("/amber-callback", amberCallback(services, log))
		authGroup.GET("/debug", debugSession(services, log))
	}

	r.GET("/api/profile", getProfile(log))

	userGroup := r.Group("/api/user")
	userGroup.Use(requireSession(services, log))
	{
		userGroup.GET("/stats", getUserStats(queries, store.Get().MaxStoragePerPubkeyBytes, log))
		userGroup.GET("/media", getUserMedia(queries, cdnBaseUrl, log))
	}

	// grain-backed API, the drive endpoints and the admin API.
	registerV1Routes(r, services, queries, store, members, log)

	// Page rendering. Everything the templates show about the server comes
	// from the live config, so admin edits appear on the next request. The
	// title falls back to the pre-config string so older frontends render
	// exactly as before.
	pageData := func(ctx *gin.Context, title string) PageData {
		conf := store.Get()
		if title == "" {
			title = conf.Server.Name
			if title == "" {
				title = "Blossom Gallery"
			}
		}
		view := ServerView{
			Name:          conf.Server.Name,
			Description:   conf.Server.Description,
			Icon:          conf.Server.Icon,
			Contact:       conf.Server.Contact,
			TermsUrl:      conf.Server.TermsUrl,
			PrivacyUrl:    conf.Server.PrivacyUrl,
			Cost:          conf.Server.Cost,
			MembershipUrl: conf.Server.MembershipUrl,
			Retention:     conf.Retention(),
			Url:           strings.TrimRight(conf.CdnUrl, "/"),
		}
		if conf.AdminPubkey != "" {
			if npub, err := tools.EncodePubkey(conf.AdminPubkey); err == nil {
				view.AdminNpub = npub
				view.AdminProfileUrl = conf.ProfileURL(npub, conf.AdminPubkey)
			}
		}
		data := PageData{Title: title, Theme: "dark", Server: view, Unclaimed: !conf.Claimed()}
		if sess := grainSession(ctx.Request); sess != nil {
			data.LoggedIn = true
			data.Pubkey = sess.PublicKey
			data.IsAdmin = conf.IsAdmin(sess.PublicKey)
		}
		return data
	}
	render := func(ctx *gin.Context, data PageData, view string) {
		renderTemplate(ctx, webFS, data, view)
	}

	// The root is always the server page (the dash). The drive lives at
	// /drive and needs a grain session; visitors without one are sent home,
	// where the sign-in button lands them back on /drive afterwards.
	r.GET("/", func(ctx *gin.Context) {
		render(ctx, pageData(ctx, ""), "index.html")
	})
	r.GET("/drive", func(ctx *gin.Context) {
		data := pageData(ctx, "")
		if !data.LoggedIn {
			ctx.Redirect(http.StatusFound, "/")
			return
		}
		render(ctx, data, "drive.html")
	})
	r.GET("/drive/settings", func(ctx *gin.Context) {
		data := pageData(ctx, "Settings")
		if !data.LoggedIn {
			ctx.Redirect(http.StatusFound, "/")
			return
		}
		render(ctx, data, "settings.html")
	})

	// First-run claim. GET shows the claim page, or who already claimed it;
	// POST makes the signed-in user the operator if nobody has yet.
	r.GET("/setup", func(ctx *gin.Context) {
		data := pageData(ctx, "Setup")
		if !data.Unclaimed {
			conf := store.Get()
			npub, _ := tools.EncodePubkey(conf.AdminPubkey)
			data.Data = map[string]string{"OwnerHex": conf.AdminPubkey, "OwnerNpub": npub}
			render(ctx, data, "setup-claimed.html")
			return
		}
		render(ctx, data, "setup.html")
	})
	r.POST("/setup", setupClaim(store, log))

	// Admin shell: operator session only. Unclaimed servers go to /setup.
	r.GET("/admin", func(ctx *gin.Context) {
		data := pageData(ctx, "Admin")
		if data.Unclaimed {
			ctx.Redirect(http.StatusFound, "/setup")
			return
		}
		if !data.IsAdmin {
			ctx.Redirect(http.StatusSeeOther, "/")
			return
		}
		data.Data = map[string]string{"ConfigPath": store.Path()}
		render(ctx, data, "admin.html")
	})

	// Legacy pages used by the 0x0 frontend.
	r.GET("/my-media", func(ctx *gin.Context) {
		render(ctx, pageData(ctx, "My Media"), "my-media.html")
	})
	r.GET("/settings", func(ctx *gin.Context) {
		render(ctx, pageData(ctx, "Settings"), "settings.html")
	})

	staticDir(r, webFS, "/static", "static")
	staticDir(r, webFS, "/scripts", "scripts")
	staticDir(r, webFS, "/res", "res")

	// Blossom protocol.
	r.HEAD("/upload", nostrAuthMiddleware("upload", log), uploadRequirements(services))
	r.PUT("/upload", nostrAuthMiddleware("upload", log), uploadBlob(services, cdnBaseUrl))
	r.PUT("/mirror", nostrAuthMiddleware("upload", log), mirrorBlob(services, cdnBaseUrl))
	r.GET("/list/:pubkey", listBlobs(services))
	r.GET("/list-all", func(ctx *gin.Context) {
		conf := store.Get()
		if !conf.PublicListingEnabled() {
			ctx.JSON(http.StatusNotFound, apiError{Message: "public listing is disabled on this server"})
			return
		}
		listAllBlobs(queries, cdnBaseUrl)(ctx)
	})
	r.GET("/stats", getStats(services))

	// Short links: /s/<code> redirects to the canonical blob URL. A temporary
	// redirect so a revoked or deleted target stops working immediately
	// instead of living on in browser caches.
	r.GET("/s/:code", func(ctx *gin.Context) {
		link, err := services.ShortLinks().Resolve(ctx.Request.Context(), ctx.Param("code"))
		if err != nil {
			ctx.String(http.StatusNotFound, "This short link does not exist or was revoked.")
			return
		}
		meta, err := services.Blob().Meta(ctx.Request.Context(), link.Hash)
		if err != nil {
			ctx.String(http.StatusNotFound, "The file behind this short link has been removed.")
			return
		}
		ctx.Header("Cache-Control", "no-store")
		ctx.Redirect(http.StatusFound, meta.Url)
	})

	r.GET("/:path", getBlob(services))
	r.HEAD("/:path", hasBlob(services))
	r.DELETE("/:path", nostrAuthMiddleware("delete", log), deleteBlob(services))

	return r
}
