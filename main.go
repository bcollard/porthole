package main

import (
	"flag"
	"fmt"
	"github.com/bcollard/porthole/pkg/controllers"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	g errgroup.Group
)

// Used by the discovery REST API
// and the debug REST API
func restRouter() http.Handler {
	restRouter := gin.Default()
	// get namespaces
	restRouter.GET("/explore", controllers.GetNamespaces)
	restRouter.GET("/explore/ns", controllers.GetNamespaces)
	restRouter.GET("/explore/namespaces", controllers.GetNamespaces)

	// get pods
	restRouter.GET("/explore/:ns", controllers.GetPods)
	restRouter.GET("/explore/ns/:ns", controllers.GetPods)
	restRouter.GET("/explore/ns/:ns/pods", controllers.GetPods)
	restRouter.GET("/explore/namespace/:ns", controllers.GetPods)
	restRouter.GET("/explore/namespace/:ns/pods", controllers.GetPods)
	restRouter.GET("/explore/namespaces/:ns", controllers.GetPods)
	restRouter.GET("/explore/namespaces/:ns/pods", controllers.GetPods)

	// debug endpoints
	restRouter.POST("/debug/inject", controllers.Inject)
	restRouter.POST("/debug/exec", controllers.Exec)
	restRouter.GET("/debug/list", controllers.List)

	return restRouter
}

// Used by the attach websocket
// and the home web page
func wsRouter() http.Handler {
	wsRouter := gin.New()
	wsRouter.GET("/echo", controllers.EchoWs)
	wsRouter.GET("/term/:ns/:pod/:ctr", controllers.AttachWs)
	wsRouter.GET("/", controllers.HomeWs)

	return wsRouter
}

func main() {

	setLogging()

	// get restPort from env
	restPort := os.Getenv("PORT")
	if restPort == "" {
		restPort = "8081"
	}

	// get wsPort from env
	wsPort := os.Getenv("WS_PORT")
	if wsPort == "" {
		wsPort = "8082"
	}

	restServer := &http.Server{
		Addr:         "0.0.0.0:" + restPort,
		Handler:      restRouter(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	wsServer := &http.Server{
		Addr:    "0.0.0.0:" + wsPort,
		Handler: wsRouter(),
	}

	g.Go(func() error {
		fmt.Printf("REST server listening on port %s\n", restPort)
		return restServer.ListenAndServe()
	})

	g.Go(func() error {
		fmt.Printf("WS server listening on port %s\n", wsPort)
		return wsServer.ListenAndServe()
	})

	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}

}

func setLogging() {
	klog.InitFlags(nil) // initializing the flags
	defer klog.Flush()  // flushes all pending log I/O

	flag.Parse() // parses the command-line flags
}
