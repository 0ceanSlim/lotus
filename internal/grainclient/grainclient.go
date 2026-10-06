// Package grainclient wires grain's Nostr client library into lotus: the
// shared outbox-routed relay pool, the cookie session manager that the
// /api/v1 handlers read, and the background cleanup routines. grain's own
// top-level client package does the same for the relay, but importing it
// pulls the relay's cgo database in, so lotus assembles the pieces itself.
package grainclient

import (
	"context"
	"time"

	"github.com/0ceanslim/grain/client/cache"
	"github.com/0ceanslim/grain/client/connection"
	"github.com/0ceanslim/grain/client/core"
	"github.com/0ceanslim/grain/client/session"
	cfgType "github.com/0ceanslim/grain/config/types"
)

// UserAgent identifies lotus to relays.
const UserAgent = "lotus-blossom/0.1"

// SessionIdleLimit is how long a session may sit unused before the cleanup
// sweep drops it. The cookie itself lasts seven days; matching that keeps a
// returning user signed in for as long as the browser does.
const SessionIdleLimit = 7 * 24 * time.Hour

// Init creates the session manager and the core client, connects to the
// index relays in the background, and starts the cleanup routines bound to
// ctx. indexRelays may be nil to use grain's built-in seed list.
func Init(ctx context.Context, indexRelays []string) error {
	session.SessionMgr = session.NewSessionManager()

	// grain reads its client settings from the relay's server config type;
	// build one that mirrors the library defaults so only the relays and the
	// user agent differ.
	d := core.DefaultConfig()
	serverCfg := &cfgType.ServerConfig{}
	serverCfg.Client = cfgType.ClientConfig{
		IndexRelays:       d.IndexRelays,
		ConnectionTimeout: int(d.ConnectionTimeout / time.Second),
		ReadTimeout:       int(d.ReadTimeout / time.Second),
		WriteTimeout:      int(d.WriteTimeout / time.Second),
		MaxConnections:    d.MaxConnections,
		RetryAttempts:     d.RetryAttempts,
		RetryDelay:        int(d.RetryDelay / time.Second),
		KeepAlive:         d.KeepAlive,
		UserAgent:         UserAgent,
	}
	if len(indexRelays) > 0 {
		serverCfg.Client.IndexRelays = indexRelays
	}

	if err := connection.InitializeCoreClient(serverCfg); err != nil {
		return err
	}

	cache.StartCacheCleanup(ctx)
	connection.StartRelayHealthCheck(ctx, 5*time.Minute)
	connection.StartRelayEvictionSweeper(ctx, time.Minute)
	go sessionCleanup(ctx)
	return nil
}

// Shutdown closes the relay pool.
func Shutdown() error {
	session.SessionMgr = nil
	return connection.CloseCoreClient()
}

func sessionCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if session.SessionMgr != nil {
				session.SessionMgr.CleanupSessions(SessionIdleLimit)
			}
		}
	}
}
