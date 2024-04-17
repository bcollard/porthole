package main

import (
	"github.com/bcollard/porthole/pkg/controllers"
	"github.com/gin-gonic/gin"
	"os"
)

func main() {
	// get port from env
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	router := gin.Default()
	// get namespaces
	router.GET("/explore", controllers.GetNamespaces)
	router.GET("/explore/ns", controllers.GetNamespaces)
	router.GET("/explore/namespaces", controllers.GetNamespaces)

	// get pods
	router.GET("/explore/:ns", controllers.GetPods)
	router.GET("/explore/ns/:ns", controllers.GetPods)
	router.GET("/explore/ns/:ns/pods", controllers.GetPods)
	router.GET("/explore/namespace/:ns", controllers.GetPods)
	router.GET("/explore/namespace/:ns/pods", controllers.GetPods)
	router.GET("/explore/namespaces/:ns", controllers.GetPods)
	router.GET("/explore/namespaces/:ns/pods", controllers.GetPods)

	// debug endpoints
	router.POST("/debug/inject", controllers.Inject)
	router.POST("/debug/exec", controllers.Exec)
	router.GET("/debug/list", controllers.List)
	router.POST("/debug/clear", controllers.Clear)

	router.Run("0.0.0.0:" + port)
}
