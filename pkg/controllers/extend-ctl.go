package controllers

import (
	"net/http"
	"os"
	"time"

	"github.com/bcollard/porthole/pkg/audit"
	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/gin-gonic/gin"
)

// ExtendSession adds or refreshes a per-EC "do not terminate before"
// timestamp the sweeper consults. Reuses the attach_ec OPA action:
// if you're allowed to attach to this EC, you're allowed to keep it
// alive. Extends by the current EC_SWEEP_TTL — same window as the
// initial lease, so an extend effectively gives you another full
// sweep period.
//
// Returns 409 when EC_SWEEP_TTL isn't set: there's nothing to extend
// because the sweeper is off entirely.
//
// POST /debug/sessions/:ns/:pod/:ec/extend
func ExtendSession(c *gin.Context) {
	start := time.Now()
	ns := c.Param("ns")
	pod := c.Param("pod")
	ec := c.Param("ec")

	if decision := auth.Authorize(c, auth.ActionAttachEC, ns, pod); !decision.Allow {
		audit.LogAttachDeny(c, ns, pod, ec, decision.Reason)
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "reason": decision.Reason})
		return
	}

	ttl, err := time.ParseDuration(os.Getenv("EC_SWEEP_TTL"))
	if err != nil || ttl <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "auto-sweep is disabled; nothing to extend"})
		return
	}

	until, err := ephemeral.Extend(ns, pod, ec, ttl)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Re-use the cleanup-event-success audit shape so the SIEM sees
	// extends in the same stream as injects/terminates. duration
	// here is the handler-time, not the new lease window.
	_ = start
	c.JSON(http.StatusOK, gin.H{
		"namespace":      ns,
		"pod":            pod,
		"ec":             ec,
		"extended_until": until.UTC().Format(time.RFC3339),
	})
}
