package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	nostr "github.com/0ceanslim/grain/server/types"
	"github.com/0ceanslim/grain/server/validation"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// blossomAuthKind is the Blossom authorization event kind (BUD-01).
const blossomAuthKind = 24242

// nostrAuthMiddleware verifies a Blossom authorization event in the
// Authorization header: a signed kind-24242 event whose t tag names the
// action, whose expiration lies in the future, and which carries an x tag
// naming the blob hash for uploads and deletes. The signer's pubkey and the
// x tag are left on the context for the handler.
func nostrAuthMiddleware(action string, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			log.Debug("[nostrAuthMiddleware] missing Authorization header")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Nostr") {
			log.Debug("[nostrAuthMiddleware] missing Nostr header prefix")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		eventBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil {
			log.Debug("[nostrAuthMiddleware] base64 decode event failed: " + err.Error())
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		var ev nostr.Event
		if err := json.Unmarshal(eventBytes, &ev); err != nil {
			log.Debug("[nostrAuthMiddleware] json decode failed: " + err.Error())
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if ev.Kind != blossomAuthKind {
			log.Debug("[nostrAuthMiddleware] invalid event kind")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if !validation.CheckSignature(ev) {
			log.Debug("[nostrAuthMiddleware] check event sig failed")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		now := time.Now().Unix()
		if ev.CreatedAt > now+60 {
			log.Debug("[nostrAuthMiddleware] invalid created_at")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		var expirationTag, tTag, xTag string
		for _, tag := range ev.Tags {
			if len(tag) != 2 {
				continue
			}
			switch tag[0] {
			case "expiration":
				expirationTag = tag[1]
			case "t":
				tTag = tag[1]
			case "x":
				xTag = tag[1]
			}
		}
		if expirationTag == "" || tTag == "" {
			log.Debug("[nostrAuthMiddleware] missing expiration or t tag")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		expiration, err := strconv.ParseInt(expirationTag, 10, 64)
		if err != nil || expiration < now {
			log.Debug("[nostrAuthMiddleware] invalid expiration")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if tTag != action {
			log.Debug("[nostrAuthMiddleware] invalid action")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if (action == "upload" || action == "delete") && xTag == "" {
			log.Debug("[nostrAuthMiddleware] action requires x tag")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		c.Set("pk", strings.ToLower(ev.PubKey))
		c.Set("x", strings.ToLower(xTag))

		c.Next()
	}
}
