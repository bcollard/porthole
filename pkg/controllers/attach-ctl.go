package controllers

import (
	"fmt"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/bcollard/porthole/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"log"
	"os"
)

var (
	upgrader = websocket.Upgrader{} // use default option
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
		err = c.WriteMessage(mt, message)

		if err != nil {
			log.Println("write err: ", err)
			break
		}
	}
}

func AttachWs(context *gin.Context) {
	namespace := context.Param("ns")
	pod := context.Param("pod")
	debugContainer := context.Param("ctr")

	w, r := context.Writer, context.Request
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	defer c.Close()

	streamz := util.Streamz{
		Input:  context.Request.Body,
		Output: w,
		Error:  w,
	}

	fmt.Printf("Attaching to %s/%s/%s...\n", namespace, pod, debugContainer)
	// ideally we would want to use a bidirectional websocket from here.
	ephemeral.Attach(context, namespace, pod, debugContainer, streamz, true)

	// for now, we will loop on incoming messages and send them to the container as exec commands :-(
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("recv: %s", message)
		err = c.WriteMessage(mt, message)

		ephemeral.Exec(context, namespace, pod, debugContainer, streamz, true, message)
		// doesn't work as expected

		if err != nil {
			log.Println("write err: ", err)
			break
		}
	}

}

func HomeWs(c *gin.Context) {
	address, port := getWsAddressAndPort()
	homeTemplate.Execute(c.Writer, "ws://"+address+":"+port)
}

func getWsAddressAndPort() (string, string) {
	address := os.Getenv("WS_ADDRESS")
	if address == "" {
		panic("WS_ADDRESS env variable is not set")
	}
	port := os.Getenv("WS_PORT")
	if port == "" {
		port = "8082"
	}
	return address, port
}
