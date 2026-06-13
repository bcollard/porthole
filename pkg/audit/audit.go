// Package audit emits one structured log line per security-relevant
// decision (inject, attach-deny, cleanup). The payload follows the
// Elastic Common Schema (ECS) v8.11 so a SIEM keyed on event.action,
// event.outcome, user.id, source.ip, kubernetes.namespace etc. catches
// every porthole event without a custom parser. Lines land on stdout,
// separate from gin's access log.
//
// User identity is read from the gin context key "user", which the
// auth middleware sets after JWT validation. Without a JWT (e.g.
// AUTH_DISABLED) the field reads "anonymous".
package audit

import (
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

const ecsVersion = "8.11"

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
	ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		// Rewrite slog's three built-in top-level keys to their ECS
		// equivalents. Leave grouped attrs alone — those are already
		// ECS-shaped by the helpers below.
		if len(groups) > 0 {
			return a
		}
		switch a.Key {
		case slog.TimeKey:
			a.Key = "@timestamp"
		case slog.LevelKey:
			a.Key = "log.level"
		case slog.MessageKey:
			a.Key = "message"
		}
		return a
	},
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

// LogInject emits the audit line for an inject attempt that wasn't
// rejected by authZ. Success → event.outcome=success event.type=creation;
// internal/k8s error → event.outcome=failure event.type=error.
func LogInject(c *gin.Context, start time.Time, namespace, pod, image, debugCtr string, err error) {
	attrs := baseAttrs(c, namespace, pod)

	containerGroup := []any{}
	if image != "" {
		containerGroup = append(containerGroup,
			slog.Group("image", slog.String("name", image)),
		)
	}

	level := slog.LevelInfo
	outcome := "success"
	etype := "creation"
	if err != nil {
		level = slog.LevelWarn
		outcome = "failure"
		etype = "error"
		attrs = append(attrs, slog.Group("error", slog.String("message", err.Error())))
	} else if debugCtr != "" {
		containerGroup = append(containerGroup, slog.String("id", debugCtr))
	}
	if len(containerGroup) > 0 {
		attrs = append(attrs, slog.Group("container", containerGroup...))
	}
	attrs = append(attrs, eventGroup("inject_ec", etype, outcome, "", start))
	logger.LogAttrs(c, level, "inject", attrs...)
}

// LogInjectDeny emits the audit line for an inject blocked by OPA.
// The reason is the OPA decision text; it lands as event.reason so a
// SIEM rule can surface the rule binding that fired.
func LogInjectDeny(c *gin.Context, start time.Time, namespace, pod, image, reason string) {
	attrs := baseAttrs(c, namespace, pod)
	if image != "" {
		attrs = append(attrs, slog.Group("container",
			slog.Group("image", slog.String("name", image)),
		))
	}
	attrs = append(attrs, eventGroup("inject_ec", "denied", "failure", reason, start))
	logger.LogAttrs(c, slog.LevelWarn, "inject", attrs...)
}

// LogAttachDeny emits one audit line when an attach is rejected by
// the authZ middleware *before* the websocket upgrade. Successful
// attaches aren't audited per-byte; the start of the session lives in
// gin's access log.
func LogAttachDeny(c *gin.Context, namespace, pod, ctr, reason string) {
	attrs := baseAttrs(c, namespace, pod)
	attrs = append(attrs, slog.Group("container", slog.String("id", ctr)))
	// No start time on the middleware path → eventGroup omits duration.
	attrs = append(attrs, eventGroup("attach_ec", "denied", "failure", reason, time.Time{}))
	logger.LogAttrs(c, slog.LevelWarn, "attach", attrs...)
}

// LogCleanup emits the audit line for cleanup attempts that weren't
// rejected by authZ. results may be nil when err is non-nil; it lands
// under porthole.results so it doesn't collide with ECS.
func LogCleanup(c *gin.Context, start time.Time, namespace, pod string, results any, err error) {
	attrs := baseAttrs(c, namespace, pod)
	level := slog.LevelInfo
	outcome := "success"
	etype := "deletion"
	if err != nil {
		level = slog.LevelWarn
		outcome = "failure"
		etype = "error"
		attrs = append(attrs, slog.Group("error", slog.String("message", err.Error())))
	}
	if results != nil {
		attrs = append(attrs, slog.Group("porthole", slog.Any("results", results)))
	}
	attrs = append(attrs, eventGroup("terminate_ec", etype, outcome, "", start))
	logger.LogAttrs(c, level, "cleanup", attrs...)
}

// LogCleanupDeny emits the audit line for a cleanup blocked by OPA.
// reason is the OPA decision text.
func LogCleanupDeny(c *gin.Context, start time.Time, namespace, pod string, results any, reason string) {
	attrs := baseAttrs(c, namespace, pod)
	if results != nil {
		attrs = append(attrs, slog.Group("porthole", slog.Any("results", results)))
	}
	attrs = append(attrs, eventGroup("terminate_ec", "denied", "failure", reason, start))
	logger.LogAttrs(c, slog.LevelWarn, "cleanup", attrs...)
}

// baseAttrs builds the ECS top-level fields every porthole audit line
// shares: ecs.version, user.id, source.ip, kubernetes.{namespace,pod.name},
// and http.request.id when the upstream proxy stamps one.
func baseAttrs(c *gin.Context, namespace, pod string) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("ecs.version", ecsVersion),
		slog.Group("user", slog.String("id", User(c))),
		slog.Group("source", slog.String("ip", c.ClientIP())),
		slog.Group("kubernetes",
			slog.String("namespace", namespace),
			slog.Group("pod", slog.String("name", pod)),
		),
	}
	if rid := c.GetHeader("X-Request-Id"); rid != "" {
		attrs = append(attrs, slog.Group("http",
			slog.Group("request", slog.String("id", rid)),
		))
	}
	return attrs
}

// eventGroup builds the ECS `event.*` block. start.IsZero() omits the
// duration field (e.g. middleware-time denies with no handler timing).
// reason is omitted when empty.
func eventGroup(action, etype, outcome, reason string, start time.Time) slog.Attr {
	attrs := []any{
		slog.String("kind", "event"),
		slog.Any("category", []string{"iam"}),
		slog.String("action", action),
		slog.String("type", etype),
		slog.String("outcome", outcome),
		slog.String("dataset", "porthole.audit"),
		slog.String("provider", "porthole"),
	}
	if !start.IsZero() {
		attrs = append(attrs, slog.Int64("duration", time.Since(start).Nanoseconds()))
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	return slog.Group("event", attrs...)
}
