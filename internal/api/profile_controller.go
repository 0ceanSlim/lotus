package api

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
	"github.com/gin-gonic/gin"
	"github.com/0ceanslim/grain/client/cache"
	"github.com/0ceanslim/grain/client/connection"
	"github.com/0ceanslim/grain/client/core"
	"github.com/0ceanslim/grain/client/core/tools"
)

type ProfileMetadata struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	About       string `json:"about"`
	Picture     string `json:"picture"`
	Banner      string `json:"banner"`
	Nip05       string `json:"nip05"`
	Lud16       string `json:"lud16"`
}

func getProfile(log *zap.Logger) gin.HandlerFunc {
	indexRelays := []string{
		"wss://purplepag.es",
		"wss://wheat.happytavern.co",
	}

	return func(ctx *gin.Context) {
		npub := ctx.Query("npub")
		if npub == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Missing npub parameter"})
			return
		}

		pubkey, err := tools.DecodeNpub(npub)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid npub"})
			return
		}

		log.Debug("Fetching profile", zap.String("npub", npub), zap.String("pubkey", pubkey))

		if cachedData, found := cache.GetUserData(pubkey); found {
			var profile ProfileMetadata
			if err := json.Unmarshal([]byte(cachedData.Metadata), &profile); err == nil {
				log.Debug("Profile cache hit", zap.String("pubkey", pubkey))
				ctx.JSON(http.StatusOK, gin.H{
					"success": true,
					"npub":    npub,
					"pubkey":  pubkey,
					"profile": profile,
					"cached":  true,
				})
				return
			}
		}

		log.Debug("Profile cache miss, fetching from relays", zap.String("pubkey", pubkey))

		coreClient := connection.GetCoreClient()
		if coreClient == nil {
			config := core.DefaultConfig()
			coreClient = core.NewClient(config)

			if err := coreClient.ConnectToRelays(indexRelays); err != nil {
				log.Warn("Failed to connect to index relays", zap.Error(err))
			}
		}

		metadataEvent, err := coreClient.GetUserProfile(pubkey, indexRelays)
		if err != nil || metadataEvent == nil {
			log.Error("Failed to fetch profile from relays", zap.String("pubkey", pubkey), zap.Error(err))
			ctx.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Profile not found on relays"})
			return
		}

		var profile ProfileMetadata
		if err := json.Unmarshal([]byte(metadataEvent.Content), &profile); err != nil {
			log.Error("Failed to parse profile metadata", zap.String("pubkey", pubkey), zap.Error(err))
			ctx.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to parse profile data"})
			return
		}

		cache.SetUserData(pubkey, metadataEvent.Content, "")

		log.Info("Successfully fetched and cached profile",
			zap.String("pubkey", pubkey),
			zap.String("display_name", profile.DisplayName),
			zap.String("name", profile.Name))

		ctx.JSON(http.StatusOK, gin.H{
			"success": true,
			"npub":    npub,
			"pubkey":  pubkey,
			"profile": profile,
			"cached":  false,
		})
	}
}
