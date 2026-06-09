package controllers

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bcollard/porthole/pkg/audit"
	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/gin-gonic/gin"
)

type InjectPayload struct {
	Namespace string `json:"namespace" binding:"required"`
	Pod       string `json:"pod" binding:"required"`
	Image     string `json:"image"`
	Command   string `json:"command"`
}

const defaultDebugImage = "nicolaka/netshoot"

// authDenyError lets the audit logger record an authZ deny as a real
// error without mixing in the rest of the kube-error string matching.
type authDenyError struct{ reason string }

func (e *authDenyError) Error() string { return "denied: " + e.reason }
func authDeny(reason string) error     { return &authDenyError{reason: reason} }

func Inject(c *gin.Context) {
	start := time.Now()
	var payload InjectPayload
	if err := c.BindJSON(&payload); err != nil {
		audit.LogInject(c, start, "", "", "", "", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	if payload.Image == "" {
		payload.Image = defaultDebugImage
	}

	if decision := auth.Authorize(c, auth.ActionInjectEC, payload.Namespace, payload.Pod); !decision.Allow {
		denyErr := authDeny(decision.Reason)
		audit.LogInject(c, start, payload.Namespace, payload.Pod, payload.Image, "", denyErr)
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "reason": decision.Reason})
		return
	}

	debugCtrName, err := ephemeral.Inject(c, payload.Namespace, payload.Pod, payload.Image, payload.Command)
	audit.LogInject(c, start, payload.Namespace, payload.Pod, payload.Image, debugCtrName, err)
	if err != nil {
		c.JSON(injectStatusFor(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"namespace":          payload.Namespace,
		"pod":                payload.Pod,
		"debugContainerName": debugCtrName,
	})
}

// injectStatusFor maps ephemeral.Inject errors to HTTP status codes. We
// can't introspect wrapped k8s api errors trivially without dragging the
// apierrors import here, so we string-match the kube error surface that
// shows up most often — NotFound when the pod is gone, Forbidden when
// RBAC denies the patch. Anything else is a 502 (upstream failure).
func injectStatusFor(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "namespace, pod and image are required"),
		strings.Contains(msg, "has no containers to target"):
		return http.StatusBadRequest
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "forbidden"):
		return http.StatusForbidden
	case strings.Contains(msg, "kube client:"):
		return http.StatusInternalServerError
	default:
		return http.StatusBadGateway
	}
}

// ListECByPath returns ephemeral containers for a pod (path-param
// variant used by the SPA).
func ListECByPath(c *gin.Context) {
	respondECList(c, c.Param("ns"), c.Param("pod"))
}

func respondECList(c *gin.Context, ns, pod string) {
	if !auth.AuthorizeOrAbort(c, auth.ActionListEC, ns, pod) {
		return
	}
	ecs, err := ephemeral.List(c, ns, pod)
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "kube client:") {
			status = http.StatusInternalServerError
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"namespace":           ns,
		"pod":                 pod,
		"ephemeralContainers": ecs,
	})
}

// GetConfig returns runtime configuration for the SPA. The WS URL is
// no longer advertised here — REST and WS share an origin now, so the
// browser derives ws[s]://<window.location.host> on its own.
//
// logoutPath comes from the LOGOUT_PATH env (default /logout) — it
// must mirror the OIDC gateway's logoutPath (e.g. Envoy Gateway's
// SecurityPolicy.oidc.logoutPath). The SPA prepends BASE_PATH to it
// when building the Logout link.
func GetConfig(c *gin.Context) {
	logoutPath := os.Getenv("LOGOUT_PATH")
	if logoutPath == "" {
		logoutPath = "/logout"
	}
	c.JSON(http.StatusOK, gin.H{
		"defaultImage": defaultDebugImage,
		"logoutPath":   logoutPath,
	})
}

// CleanupOne terminates a single porthole-injected ephemeral
// container. Non-porthole-* EC names are refused at the
// ephemeral-package boundary (TerminateByName).
func CleanupOne(c *gin.Context) {
	start := time.Now()
	ns := c.Param("ns")
	pod := c.Param("pod")
	ec := c.Param("ec")
	if !auth.AuthorizeOrAbort(c, auth.ActionTerminateEC, ns, pod) {
		audit.LogCleanup(c, start, ns, pod, map[string]string{"ec": ec}, authDeny("authz"))
		return
	}
	if err := ephemeral.TerminateByName(c, ns, pod, ec); err != nil {
		audit.LogCleanup(c, start, ns, pod, map[string]string{"ec": ec}, err)
		status := http.StatusBadGateway
		switch {
		case strings.Contains(err.Error(), "not found"):
			status = http.StatusNotFound
		case strings.Contains(err.Error(), "refusing to terminate"):
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	audit.LogCleanup(c, start, ns, pod, map[string]string{"ec": ec}, nil)
	c.JSON(http.StatusOK, gin.H{
		"namespace": ns,
		"pod":       pod,
		"ec":        ec,
		"ok":        true,
	})
}

// Cleanup terminates every running porthole-injected ephemeral
// container in the targeted pod.
func Cleanup(c *gin.Context) {
	start := time.Now()
	ns := c.Param("ns")
	pod := c.Param("pod")
	if !auth.AuthorizeOrAbort(c, auth.ActionTerminateEC, ns, pod) {
		audit.LogCleanup(c, start, ns, pod, nil, authDeny("authz"))
		return
	}
	results, err := ephemeral.Cleanup(c, ns, pod)
	audit.LogCleanup(c, start, ns, pod, results, err)
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"namespace": ns,
		"pod":       pod,
		"results":   results,
	})
}
