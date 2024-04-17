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
	router.GET("/namespaces", controllers.GetNamespaces)
	router.GET("/namespaces/:ns/pods", controllers.GetPods)
	router.GET("/namespace/:ns/pods", controllers.GetPods)

	router.Run("0.0.0.0:" + port)
}
