package controllers

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/bcollard/porthole/pkg/audit"
	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/bcollard/porthole/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: checkOrigin,
	}

	allowedOrigins = parseAllowedOrigins(os.Getenv("WS_ALLOWED_ORIGINS"))
)

// checkOrigin gates WebSocket upgrades to defend against cross-site
// WebSocket hijacking. Browsers always send Origin on WS upgrades; an
// empty Origin means a non-browser client (curl/wscat/etc.) which
// can't be cookie-CSRF'd, so we let those through. When
// WS_ALLOWED_ORIGINS is set, only origins in that comma-separated
// list are accepted. Otherwise we fall back to a same-origin check
// (Origin's host must match the request's Host).
func checkOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	if len(allowedOrigins) > 0 {
		return slices.Contains(allowedOrigins, o)
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

func parseAllowedOrigins(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func AttachWs(c *gin.Context) {
	namespace := c.Param("ns")
	pod := c.Param("pod")
	debugContainer := c.Param("ctr")

	// Authorize BEFORE upgrading — a 403 lets the browser surface the
	// reason via ws.onerror; after upgrade we'd lose that affordance.
	if d := auth.Authorize(c, auth.ActionAttachEC, namespace, pod); !d.Allow {
		audit.LogAttachDeny(c, namespace, pod, debugContainer, d.Reason)
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "reason": d.Reason})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	defer conn.Close()

	session := util.NewWsSession(conn)
	go session.Start(c.Request.Context())

	streamz := util.Streamz{
		Input:  session.Stdin(),
		Output: session.Stdout(),
		Error:  session.Stderr(),
	}

	fmt.Printf("Attaching to %s/%s/%s...\n", namespace, pod, debugContainer)
	ephemeral.Attach(c, namespace, pod, debugContainer, streamz, session.Resize(), true)
}
