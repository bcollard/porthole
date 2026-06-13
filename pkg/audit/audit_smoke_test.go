package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestAuditShape prints one line of each audit variant so a human (or
// the test runner with -v) can eyeball the ECS shape. No assertions —
// it's a smoke test to keep the README sample honest.
func TestAuditShape(t *testing.T) {
	var buf bytes.Buffer
	orig := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				return slog.Attr{Key: "@timestamp", Value: slog.StringValue("2026-06-11T10:53:45Z")}
			case slog.LevelKey:
				a.Key = "log.level"
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	}))
	t.Cleanup(func() { logger = orig })

	gin.SetMode(gin.TestMode)
	mkCtx := func(user string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/inject", nil)
		c.Request.Header.Set("X-Forwarded-For", "10.0.1.5")
		c.Request.Header.Set("X-Request-Id", "req-abc123")
		c.Set("user", user)
		return c
	}

	start := time.Now().Add(-53 * time.Millisecond)

	cases := []struct {
		name string
		emit func()
	}{
		{"inject_success", func() {
			LogInject(mkCtx("alice@example.com"), start,
				"team-a-prod", "checkout-api-7d4f9bc8d-xk2p9",
				"nicolaka/netshoot:latest", "porthole-a2045e61", nil)
		}},
		{"inject_denied", func() {
			LogInjectDeny(mkCtx("bob@example.com"), start,
				"team-a-prod", "checkout-api-7d4f9bc8d-xk2p9",
				"nicolaka/netshoot:latest",
				"no binding grants inject_ec on team-a-prod for group team-b")
		}},
		{"inject_error", func() {
			LogInject(mkCtx("alice@example.com"), start,
				"team-a-prod", "checkout-api-7d4f9bc8d-xk2p9",
				"nicolaka/netshoot:latest", "",
				errors.New("kube client: pod not found"))
		}},
		{"attach_denied", func() {
			LogAttachDeny(mkCtx("bob@example.com"),
				"team-a-prod", "checkout-api-7d4f9bc8d-xk2p9",
				"porthole-a2045e61",
				"no binding grants attach_ec on team-a-prod for group team-b")
		}},
		{"cleanup_success", func() {
			LogCleanup(mkCtx("alice@example.com"), start,
				"team-a-prod", "checkout-api-7d4f9bc8d-xk2p9",
				map[string]any{"terminated": []string{"porthole-a2045e61"}}, nil)
		}},
		{"cleanup_denied", func() {
			LogCleanupDeny(mkCtx("bob@example.com"), start,
				"team-a-prod", "checkout-api-7d4f9bc8d-xk2p9",
				nil, "authz")
		}},
	}

	for _, tc := range cases {
		buf.Reset()
		tc.emit()
		line := strings.TrimSpace(buf.String())
		// Pretty-print so eyeballing the ECS structure is easy.
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("%s: not valid JSON: %v\n%s", tc.name, err, line)
		}
		pretty, _ := json.MarshalIndent(v, "", "  ")
		t.Logf("--- %s ---\n%s", tc.name, pretty)
	}
}
