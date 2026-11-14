package api

import (
	"net/http"
	"runtime/debug"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// accessLog chooses, per request, between ginzap's standard access line and
// one without the client address. The upload limiter keeps addresses out of
// memory except as keyed hashes; logging them beside every upload would undo
// that, so while it is on the access log drops the ip field. The choice is
// made per request because rate_limit.enabled is hot.
func accessLog(log *zap.Logger, private func() bool) gin.HandlerFunc {
	standard := ginzap.Ginzap(log, time.RFC3339, true)
	noAddr := privateAccessLog(log)
	return func(c *gin.Context) {
		if private() {
			noAddr(c)
			return
		}
		standard(c)
	}
}

// privateAccessLog mirrors ginzap's fields minus the client address.
func privateAccessLog(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		c.Next()
		end := time.Now()
		fields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.Duration("latency", end.Sub(start)),
			zap.String("time", end.UTC().Format(time.RFC3339)),
		}
		if len(c.Errors) > 0 {
			for _, e := range c.Errors.Errors() {
				log.Error(e, fields...)
			}
			return
		}
		log.Info("", fields...)
	}
}

// recovery is the panic handler counterpart of accessLog: ginzap's recovery
// dumps the request, headers included, which would log the forwarded client
// address; the private variant logs the route and the stack only.
func recovery(log *zap.Logger, private func() bool) gin.HandlerFunc {
	standard := ginzap.RecoveryWithZap(log, true)
	noAddr := privateRecovery(log)
	return func(c *gin.Context) {
		if private() {
			noAddr(c)
			return
		}
		standard(c)
	}
}

func privateRecovery(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Error("[Recovery from panic]",
					zap.Time("time", time.Now()),
					zap.Any("error", err),
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
					zap.String("stack", string(debug.Stack())),
				)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
