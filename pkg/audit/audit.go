// Package audit emits one structured log line per security-relevant
// decision (currently: ephemeral-container injections). The intent is
// that a SIEM / log shipper picks these up by their fixed "action"
// key. JSON is written to stdout, separate from gin's access log.
//
// User identity is read from the gin context key "user", which the
// auth middleware will set after JWT validation. Until that lands,
// the field is "anonymous".
package audit

import (
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// User returns the authenticated identity, or "anonymous" if no auth
// middleware has stamped one onto the request context.
func User(c *gin.Context) string {
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "anonymous"
}

// LogAttachDeny emits one audit line when an attach is rejected by
// the authZ middleware *before* the websocket upgrade. Successful
// attaches aren't audited per-byte; the start of the session lives in
// gin's access log.
func LogAttachDeny(c *gin.Context, namespace, pod, ctr, reason string) {
	logger.LogAttrs(c, slog.LevelWarn, "attach",
		slog.String("action", "attach_ec"),
		slog.String("user", User(c)),
		slog.String("source_ip", c.ClientIP()),
		slog.String("namespace", namespace),
		slog.String("pod", pod),
		slog.String("debug_container", ctr),
		slog.String("outcome", "denied"),
		slog.String("reason", reason),
	)
}

// LogInject emits the audit line for one inject decision. Call it
// once per request — on both success and failure — so denied
// attempts are recorded too. namespace/pod/image may be empty when
// the request was rejected before parsing.
func LogInject(c *gin.Context, start time.Time, namespace, pod, image, debugCtr string, err error) {
	attrs := []slog.Attr{
		slog.String("action", "inject"),
		slog.String("user", User(c)),
		slog.String("source_ip", c.ClientIP()),
		slog.String("namespace", namespace),
		slog.String("pod", pod),
		slog.String("image", image),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	}
	if rid := c.GetHeader("X-Request-Id"); rid != "" {
		attrs = append(attrs, slog.String("request_id", rid))
	}
	if err == nil {
		attrs = append(attrs,
			slog.String("outcome", "success"),
			slog.String("debug_container", debugCtr),
		)
		logger.LogAttrs(c, slog.LevelInfo, "inject", attrs...)
		return
	}
	attrs = append(attrs,
		slog.String("outcome", "error"),
		slog.String("error", err.Error()),
	)
	logger.LogAttrs(c, slog.LevelWarn, "inject", attrs...)
}
