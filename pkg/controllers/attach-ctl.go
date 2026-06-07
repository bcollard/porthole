package controllers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/bcollard/porthole/pkg/audit"
	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/bcollard/porthole/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func EchoWs(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	defer c.Close()

	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("recv: %s", message)
		if err := c.WriteMessage(mt, message); err != nil {
			log.Println("write err: ", err)
			break
		}
	}
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
