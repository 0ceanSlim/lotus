package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/core"
)

func sessionMiddleware(services core.Services, log *zap.Logger, required bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		sessionID, err := ctx.Cookie(SessionCookieName)
		if err != nil {
			if required {
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": "Authentication required",
				})
				return
			}
			ctx.Next()
			return
		}

		sessionService := services.Session()

		session, err := sessionService.GetSession(ctx.Request.Context(), sessionID)
		if err != nil {
			if required {
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": "Invalid or expired session",
				})
				return
			}
			ctx.Next()
			return
		}

		_ = sessionService.UpdateLastActive(ctx.Request.Context(), sessionID)

		ctx.Set("session_id", session.ID)
		ctx.Set("public_key", session.PublicKey)
		ctx.Set("npub", session.Npub)
		ctx.Set("signing_method", session.SigningMethod)
		ctx.Set("session_mode", session.Mode)

		ctx.Next()
	}
}

func requireSession(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return sessionMiddleware(services, log, true)
}
