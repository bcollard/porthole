package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/controllers"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/bcollard/porthole/pkg/web"
	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

func router(jwtMW gin.HandlerFunc) http.Handler {
	r := gin.Default()
	r.Use(corsMiddleware())

	// ----- public (no auth) -----
	r.StaticFS("/ui", web.FS())
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/ui/") })
	r.GET("/index.html", func(c *gin.Context) { c.FileFromFS("index.html", web.FS()) })
	r.GET("/app.js", func(c *gin.Context) { c.FileFromFS("app.js", web.FS()) })
	r.GET("/style.css", func(c *gin.Context) { c.FileFromFS("style.css", web.FS()) })
	r.GET("/api/config", controllers.GetConfig)

	// ----- protected (JWT required, OPA-authorized inside the handlers) -----
	api := r.Group("/")
	api.Use(jwtMW)

	api.GET("/explore", controllers.GetNamespaces)
	api.GET("/explore/ns", controllers.GetNamespaces)
	api.GET("/explore/namespaces", controllers.GetNamespaces)

	api.GET("/explore/:ns", controllers.GetPods)
	api.GET("/explore/ns/:ns", controllers.GetPods)
	api.GET("/explore/ns/:ns/pods", controllers.GetPods)
	api.GET("/explore/namespace/:ns", controllers.GetPods)
	api.GET("/explore/namespace/:ns/pods", controllers.GetPods)
	api.GET("/explore/namespaces/:ns", controllers.GetPods)
	api.GET("/explore/namespaces/:ns/pods", controllers.GetPods)

	api.GET("/explore/ns/:ns/pods/:pod/ec", controllers.ListECByPath)

	api.POST("/debug/inject", controllers.Inject)
	api.GET("/debug/list", controllers.List)
	api.POST("/debug/cleanup/:ns/:pod", controllers.Cleanup)

	api.GET("/term/:ns/:pod/:ctr", controllers.AttachWs)

	return r
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-ID-Token")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func main() {
	setLogging()

	jwtMW, err := auth.NewJWTMiddleware(auth.JWTConfig{
		JWKSURL:  os.Getenv("JWKS_URL"),
		Issuer:   os.Getenv("OIDC_ISSUER"),
		Audience: os.Getenv("OIDC_AUDIENCE"),
	})
	if err != nil {
		log.Fatalf("auth init: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	sweepTTL, _ := time.ParseDuration(os.Getenv("EC_SWEEP_TTL"))
	ephemeral.StartSweeper(context.Background(), ephemeral.SweepConfig{TTL: sweepTTL})

	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           router(jwtMW),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logStartupBanner(port, sweepTTL)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func logStartupBanner(port string, sweepTTL time.Duration) {
	authMode := "JWT required"
	if os.Getenv("AUTH_DISABLED") == "true" {
		authMode = "AUTH_DISABLED (local-dev principal)"
	}
	opaMode := "OPA disabled (allow all)"
	if u := os.Getenv("OPA_URL"); u != "" {
		opaMode = "OPA @ " + u
	}
	sweepMode := "EC sweeper disabled"
	if sweepTTL > 0 {
		sweepMode = "EC sweeper TTL=" + sweepTTL.String()
	}
	fmt.Printf("Porthole listening on http://0.0.0.0:%s/ui/\n", port)
	fmt.Printf("  authN: %s\n", authMode)
	fmt.Printf("  authZ: %s\n", opaMode)
	fmt.Printf("  sweep: %s\n", sweepMode)
}

func setLogging() {
	klog.InitFlags(nil)
	defer klog.Flush()
	flag.Parse()
}
