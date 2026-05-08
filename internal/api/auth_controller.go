package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/0ceanSlim/lotus/internal/core"
	"go.uber.org/zap"
)

const (
	SessionCookieName = "blossom_session"
	SessionCookieAge  = 86400 // 24 hours in seconds
)

// Login request
type loginRequest struct {
	PublicKey     string `json:"public_key"`
	PrivateKey    string `json:"private_key,omitempty"`
	SigningMethod string `json:"signing_method"` // 'browser_extension', 'amber', 'encrypted_key'
	Mode          string `json:"mode"`           // 'read' or 'write'
}

// Login response
type loginResponse struct {
	Success bool              `json:"success"`
	Message string            `json:"message,omitempty"`
	Session *sessionInfo      `json:"session,omitempty"`
	Npub    string            `json:"npub,omitempty"`
}

// Session info
type sessionInfo struct {
	PublicKey     string `json:"public_key"`
	SigningMethod string `json:"signing_method"`
	Mode          string `json:"mode"`
}

// Session check response
type sessionCheckResponse struct {
	Success  bool         `json:"success"`
	IsActive bool         `json:"is_active"`
	Session  *sessionInfo `json:"session,omitempty"`
	Npub     string       `json:"npub,omitempty"`
}

// Key generation response
type keyGenResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	KeyPair *keyPairInfo    `json:"key_pair,omitempty"`
}

// Key pair info
type keyPairInfo struct {
	Npub       string `json:"npub"`
	Nsec       string `json:"nsec"`
	HexPubkey  string `json:"hex_pubkey"`
	HexPrivkey string `json:"hex_privkey"`
}

// POST /api/auth/login
func login(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req loginRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, loginResponse{
				Success: false,
				Message: "Invalid request body",
			})
			return
		}

		// Validate required fields
		if req.SigningMethod == "" {
			ctx.JSON(http.StatusBadRequest, loginResponse{
				Success: false,
				Message: "signing_method is required",
			})
			return
		}

		if req.Mode == "" {
			req.Mode = "write" // Default to write mode
		}

		sessionService := services.Session()

		var publicKey string
		var err error

		// Handle different signing methods
		switch req.SigningMethod {
		case "browser_extension", "amber":
			// Public key should be provided
			if req.PublicKey == "" {
				ctx.JSON(http.StatusBadRequest, loginResponse{
					Success: false,
					Message: "public_key is required for browser_extension and amber",
				})
				return
			}
			publicKey, err = sessionService.DecodePublicKey(req.PublicKey)
			if err != nil {
				ctx.JSON(http.StatusBadRequest, loginResponse{
					Success: false,
					Message: "Invalid public key format: " + err.Error(),
				})
				return
			}

		case "encrypted_key":
			// Private key should be provided
			if req.PrivateKey == "" {
				ctx.JSON(http.StatusBadRequest, loginResponse{
					Success: false,
					Message: "private_key is required for encrypted_key method",
				})
				return
			}
			// Derive public key from private key
			publicKey, err = sessionService.GetPublicKeyFromPrivateKey(req.PrivateKey)
			if err != nil {
				ctx.JSON(http.StatusBadRequest, loginResponse{
					Success: false,
					Message: "Invalid private key: " + err.Error(),
				})
				return
			}

		default:
			ctx.JSON(http.StatusBadRequest, loginResponse{
				Success: false,
				Message: "Invalid signing_method. Must be 'browser_extension', 'amber', or 'encrypted_key'",
			})
			return
		}

		// Create session
		session, err := sessionService.CreateSession(
			ctx.Request.Context(),
			publicKey,
			req.SigningMethod,
			req.Mode,
		)
		if err != nil {
			log.Error("failed to create session", zap.Error(err))
			ctx.JSON(http.StatusInternalServerError, loginResponse{
				Success: false,
				Message: "Failed to create session",
			})
			return
		}

		// Set session cookie (http-only, secure in production)
		ctx.SetCookie(
			SessionCookieName,
			session.ID,
			SessionCookieAge,
			"/",
			"",
			false, // Set to true in production with HTTPS
			true,  // HTTP-only
		)

		ctx.JSON(http.StatusOK, loginResponse{
			Success: true,
			Session: &sessionInfo{
				PublicKey:     session.PublicKey,
				SigningMethod: session.SigningMethod,
				Mode:          session.Mode,
			},
			Npub: session.Npub,
		})
	}
}

// GET /api/auth/session
func checkSession(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Get session ID from cookie
		sessionID, err := ctx.Cookie(SessionCookieName)
		if err != nil {
			ctx.JSON(http.StatusOK, sessionCheckResponse{
				Success:  true,
				IsActive: false,
			})
			return
		}

		sessionService := services.Session()

		// Get session
		session, err := sessionService.GetSession(ctx.Request.Context(), sessionID)
		if err != nil {
			ctx.JSON(http.StatusOK, sessionCheckResponse{
				Success:  true,
				IsActive: false,
			})
			return
		}

		// Update last active
		_ = sessionService.UpdateLastActive(ctx.Request.Context(), sessionID)

		ctx.JSON(http.StatusOK, sessionCheckResponse{
			Success:  true,
			IsActive: true,
			Session: &sessionInfo{
				PublicKey:     session.PublicKey,
				SigningMethod: session.SigningMethod,
				Mode:          session.Mode,
			},
			Npub: session.Npub,
		})
	}
}

// POST /api/auth/logout
func logout(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Get session ID from cookie
		sessionID, err := ctx.Cookie(SessionCookieName)
		if err != nil {
			// No session, but that's okay
			ctx.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "Logged out",
			})
			return
		}

		sessionService := services.Session()

		// Delete session from database
		_ = sessionService.DeleteSession(ctx.Request.Context(), sessionID)

		// Clear cookie
		ctx.SetCookie(
			SessionCookieName,
			"",
			-1, // Expire immediately
			"/",
			"",
			false,
			true,
		)

		ctx.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Logged out successfully",
		})
	}
}

// POST /api/auth/generate-keys
func generateKeys(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		sessionService := services.Session()

		keyPair, err := sessionService.GenerateKeyPair()
		if err != nil {
			log.Error("failed to generate keys", zap.Error(err))
			ctx.JSON(http.StatusInternalServerError, keyGenResponse{
				Success: false,
				Message: "Failed to generate keys",
			})
			return
		}

		ctx.JSON(http.StatusOK, keyGenResponse{
			Success: true,
			KeyPair: &keyPairInfo{
				Npub:       keyPair.Npub,
				Nsec:       keyPair.Nsec,
				HexPubkey:  keyPair.HexPubkey,
				HexPrivkey: keyPair.HexPrivkey,
			},
		})
	}
}

// GET /api/auth/debug
// Debug endpoint to check session cache
func debugSession(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Get session ID from cookie
		sessionID, err := ctx.Cookie(SessionCookieName)
		if err != nil {
			ctx.JSON(http.StatusOK, gin.H{
				"success":    true,
				"has_cookie": false,
				"message":    "No session cookie found",
			})
			return
		}

		sessionService := services.Session()

		// Get session
		session, err := sessionService.GetSession(ctx.Request.Context(), sessionID)
		if err != nil {
			ctx.JSON(http.StatusOK, gin.H{
				"success":    true,
				"has_cookie": true,
				"session_id": sessionID,
				"is_valid":   false,
				"error":      err.Error(),
			})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"success":        true,
			"has_cookie":     true,
			"session_id":     sessionID,
			"is_valid":       true,
			"public_key":     session.PublicKey,
			"npub":           session.Npub,
			"signing_method": session.SigningMethod,
			"mode":           session.Mode,
			"created_at":     session.CreatedAt,
			"expires_at":     session.ExpiresAt,
			"last_active":    session.LastActive,
		})
	}
}

// GET /api/auth/amber-callback
// Handles Amber (NIP-55) callback
func amberCallback(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		log.Info("Amber callback received", zap.String("url", ctx.Request.URL.String()))

		// Get the result from query parameter - Amber may use different param names
		eventParam := ctx.Query("event")
		if eventParam == "" {
			eventParam = ctx.Query("signature")
		}
		if eventParam == "" {
			eventParam = ctx.Query("result")
		}
		if eventParam == "" {
			// Check if pubkey was sent directly
			eventParam = ctx.Query("pubkey")
		}
		if eventParam == "" {
			log.Error("Amber callback missing event parameter", zap.String("query", ctx.Request.URL.RawQuery))
			renderAmberError(ctx, "Missing event data from Amber. Query: "+ctx.Request.URL.RawQuery)
			return
		}

		log.Info("Amber event parameter", zap.String("event", eventParam))

		// Extract public key from the event parameter
		publicKey := extractPublicKeyFromAmber(eventParam, log)
		if publicKey == "" {
			log.Error("Failed to extract public key from amber response")
			renderAmberError(ctx, "Invalid response from Amber - could not extract public key")
			return
		}

		log.Info("Amber callback processed successfully", zap.String("pubkey", publicKey[:16]+"..."))

		// Create session
		sessionService := services.Session()
		session, err := sessionService.CreateSession(
			ctx.Request.Context(),
			publicKey,
			"amber",
			"write",
		)
		if err != nil {
			log.Error("Failed to create amber session", zap.Error(err))
			renderAmberError(ctx, "Failed to create session")
			return
		}

		// Set session cookie
		ctx.SetCookie(
			SessionCookieName,
			session.ID,
			SessionCookieAge,
			"/",
			"",
			false,
			true,
		)

		log.Info("Amber session created successfully", zap.String("pubkey", publicKey[:16]+"..."))

		// Render success page
		renderAmberSuccess(ctx, publicKey, session.Npub)
	}
}

// extractPublicKeyFromAmber extracts the public key from Amber's response
func extractPublicKeyFromAmber(eventParam string, log *zap.Logger) string {
	// Try to parse as JSON event
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(eventParam), &event); err == nil {
		// It's a JSON event, extract pubkey field
		if pubkey, ok := event["pubkey"].(string); ok && len(pubkey) == 64 {
			log.Info("Extracted pubkey from JSON event", zap.String("pubkey", pubkey))
			return pubkey
		}

		// Check if it's wrapped in an "event" field
		if eventObj, ok := event["event"].(map[string]interface{}); ok {
			if pubkey, ok := eventObj["pubkey"].(string); ok && len(pubkey) == 64 {
				log.Info("Extracted pubkey from nested event", zap.String("pubkey", pubkey))
				return pubkey
			}
		}

		log.Warn("JSON event doesn't have valid pubkey field")
		return ""
	}

	// Fallback: treat as direct public key string
	publicKey := strings.TrimSpace(eventParam)

	// Validate public key format (64 hex characters)
	if len(publicKey) == 64 {
		for _, c := range publicKey {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				log.Warn("Not a valid hex pubkey", zap.String("value", publicKey))
				return ""
			}
		}
		log.Info("Extracted pubkey as direct string", zap.String("pubkey", publicKey))
		return publicKey
	}

	log.Warn("Invalid pubkey format", zap.String("value", publicKey))
	return ""
}

// renderAmberSuccess renders a success page that stores result in localStorage
func renderAmberSuccess(ctx *gin.Context, publicKey string, npub string) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Amber Login Success</title>
    <style>
        body {
            font-family: system-ui, sans-serif;
            margin: 0;
            padding: 20px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            text-align: center;
            min-height: 100vh;
            display: flex;
            flex-direction: column;
            justify-content: center;
        }
        .success { margin: 20px 0; }
        h2 { color: #90EE90; }
    </style>
</head>
<body>
    <div class="success">
        <h2>Amber Login Successful!</h2>
        <p>Returning to Blossom...</p>
    </div>

    <script>
        const amberResult = {
            success: true,
            publicKey: '` + publicKey + `',
            npub: '` + npub + `',
            timestamp: Date.now()
        };

        try {
            localStorage.setItem('amber_callback_result', JSON.stringify(amberResult));
            console.log('Stored Amber success result in localStorage');
        } catch (error) {
            console.error('Failed to store Amber result:', error);
        }

        // Try to notify opener window
        if (window.opener && !window.opener.closed) {
            try {
                window.opener.postMessage({
                    type: 'amber_success',
                    publicKey: '` + publicKey + `',
                    npub: '` + npub + `'
                }, window.location.origin);
            } catch (error) {
                console.error('Failed to send message to opener:', error);
            }
        }

        // Close or redirect
        setTimeout(() => {
            try {
                if (window.opener && !window.opener.closed) {
                    window.close();
                } else {
                    window.location.href = '/?amber_login=success';
                }
            } catch (error) {
                window.location.href = '/';
            }
        }, 1500);
    </script>
</body>
</html>`

	ctx.Header("Content-Type", "text/html; charset=utf-8")
	ctx.String(http.StatusOK, html)
}

// renderAmberError renders an error page for Amber callback failures
func renderAmberError(ctx *gin.Context, errorMsg string) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Amber Login Error</title>
    <style>
        body {
            font-family: system-ui, sans-serif;
            margin: 0;
            padding: 20px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            text-align: center;
            min-height: 100vh;
            display: flex;
            flex-direction: column;
            justify-content: center;
        }
        .error { color: #ff6b6b; margin: 20px 0; }
        a { color: #90EE90; }
    </style>
</head>
<body>
    <div class="error">
        <h2>Amber Login Failed</h2>
        <p>` + errorMsg + `</p>
    </div>
    <p><a href="/">Return to Blossom</a></p>

    <script>
        const amberResult = {
            success: false,
            error: '` + errorMsg + `',
            timestamp: Date.now()
        };

        try {
            localStorage.setItem('amber_callback_result', JSON.stringify(amberResult));
        } catch (error) {
            console.error('Failed to store error result:', error);
        }

        if (window.opener) {
            window.opener.postMessage({
                type: 'amber_error',
                error: '` + errorMsg + `'
            }, window.location.origin);
            setTimeout(() => window.close(), 3000);
        }
    </script>
</body>
</html>`

	ctx.Header("Content-Type", "text/html; charset=utf-8")
	ctx.String(http.StatusBadRequest, html)
}
