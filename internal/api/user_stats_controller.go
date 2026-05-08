package api

import (
	"net/http"

	"go.uber.org/zap"
	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/db"
)

type UserStats struct {
	TotalStorage int64           `json:"total_storage"`
	FileCount    int64           `json:"file_count"`
	QuotaBytes   int64           `json:"quota_bytes"`
	PercentUsed  float64         `json:"percent_used"`
	FileTypes    []FileTypeStats `json:"file_types"`
}

type FileTypeStats struct {
	MimeType  string `json:"mime_type"`
	Count     int64  `json:"count"`
	TotalSize int64  `json:"total_size"`
}

func getUserStats(queries *db.Queries, quotaBytes int64, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey, exists := ctx.Get("public_key")
		if !exists {
			ctx.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "Not authenticated"})
			return
		}
		pubkeyStr := pubkey.(string)

		totalStorage, err := queries.GetTotalStorageByPubkey(ctx.Request.Context(), pubkeyStr)
		if err != nil {
			log.Error("Failed to get total storage", zap.Error(err))
			ctx.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get storage stats"})
			return
		}

		blobs, err := queries.GetBlobsFromPubkey(ctx.Request.Context(), pubkeyStr)
		if err != nil {
			log.Error("Failed to get blobs", zap.Error(err))
			ctx.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get blob list"})
			return
		}

		fileCount := int64(len(blobs))

		typeMap := make(map[string]*FileTypeStats)
		for _, blob := range blobs {
			if stats, exists := typeMap[blob.Type]; exists {
				stats.Count++
				stats.TotalSize += blob.Size
			} else {
				typeMap[blob.Type] = &FileTypeStats{
					MimeType:  blob.Type,
					Count:     1,
					TotalSize: blob.Size,
				}
			}
		}

		fileTypesStats := make([]FileTypeStats, 0, len(typeMap))
		for _, stats := range typeMap {
			fileTypesStats = append(fileTypesStats, *stats)
		}

		percentUsed := 0.0
		if quotaBytes > 0 {
			percentUsed = (float64(totalStorage) / float64(quotaBytes)) * 100
		}

		stats := UserStats{
			TotalStorage: totalStorage,
			FileCount:    fileCount,
			QuotaBytes:   quotaBytes,
			PercentUsed:  percentUsed,
			FileTypes:    fileTypesStats,
		}

		ctx.JSON(http.StatusOK, gin.H{"success": true, "stats": stats})
	}
}

func getUserMedia(queries *db.Queries, cdnBaseUrl string, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey, exists := ctx.Get("public_key")
		if !exists {
			ctx.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "Not authenticated"})
			return
		}
		pubkeyStr := pubkey.(string)

		blobs, err := queries.GetBlobsFromPubkey(ctx.Request.Context(), pubkeyStr)
		if err != nil {
			log.Error("Failed to get blobs", zap.Error(err))
			ctx.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get media list"})
			return
		}

		type MediaItem struct {
			SHA256   string `json:"sha256"`
			URL      string `json:"url"`
			Type     string `json:"type"`
			Size     int64  `json:"size"`
			Uploaded int64  `json:"uploaded"`
		}

		mediaItems := make([]MediaItem, len(blobs))
		for i, blob := range blobs {
			mediaItems[i] = MediaItem{
				SHA256:   blob.Hash,
				URL:      cdnBaseUrl + "/" + blob.Hash,
				Type:     blob.Type,
				Size:     blob.Size,
				Uploaded: blob.Created,
			}
		}

		ctx.JSON(http.StatusOK, gin.H{"success": true, "media": mediaItems, "count": len(mediaItems)})
	}
}
