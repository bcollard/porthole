package controllers

import (
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
		log.Printf("recv:%s", message)
		err = c.WriteMessage(mt, message)
		if err != nil {
			log.Println("write:", err)
			break
		}
	}
}

func HomeWs(c *gin.Context) {
	address, port := getWsAddressAndPort()
	homeTemplate.Execute(c.Writer, "ws://"+address+":"+port+"/echo")
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
