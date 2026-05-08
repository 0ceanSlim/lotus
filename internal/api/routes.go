package api

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

func SetupRoutes(
	services core.Services,
	queries *db.Queries,
	cdnBaseUrl string,
	adminPubkey string,
	maxStoragePerPubkey int64,
	log *zap.Logger,
	dataDir string,
) *gin.Engine {
	webDir := filepath.Join(dataDir, "web")
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
		userGroup.GET("/stats", getUserStats(queries, maxStoragePerPubkey, log))
		userGroup.GET("/media", getUserMedia(queries, cdnBaseUrl, log))
	}

	render := func(ctx *gin.Context, data PageData, view string) {
		renderTemplate(ctx, webDir, data, view)
	}

	r.GET("/", func(ctx *gin.Context) {
		render(ctx, PageData{Title: "Blossom Gallery", Theme: "dark"}, "index.html")
	})
	r.GET("/gallery", func(ctx *gin.Context) {
		render(ctx, PageData{Title: "Blossom Gallery", Theme: "dark"}, "index.html")
	})
	r.GET("/gallery/", func(ctx *gin.Context) {
		render(ctx, PageData{Title: "Blossom Gallery", Theme: "dark"}, "index.html")
	})
	r.GET("/my-media", func(ctx *gin.Context) {
		render(ctx, PageData{Title: "My Media", Theme: "dark"}, "my-media.html")
	})
	r.GET("/settings", func(ctx *gin.Context) {
		render(ctx, PageData{Title: "Settings", Theme: "dark"}, "settings.html")
	})

	r.Static("/scripts", webDir+"/scripts")
	r.Static("/res", webDir+"/res")
	r.Static("/gallery/assets", webDir+"/assets")

	r.HEAD("/upload", nostrAuthMiddleware("upload", log), uploadRequirements(services))
	r.PUT("/upload", nostrAuthMiddleware("upload", log), uploadBlob(services, cdnBaseUrl))
	r.PUT("/mirror", nostrAuthMiddleware("upload", log), mirrorBlob(services, cdnBaseUrl))
	r.GET("/list/:pubkey", listBlobs(services))
	r.GET("/list-all", listAllBlobs(queries, cdnBaseUrl))
	r.GET("/stats", getStats(services))

	r.GET("/:path", getBlob(services))
	r.HEAD("/:path", hasBlob(services))
	r.DELETE("/:path", nostrAuthMiddleware("delete", log), deleteBlob(services))

	return r
}
