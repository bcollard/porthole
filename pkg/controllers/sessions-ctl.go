package controllers

import (
	"net/http"
	"strings"
	"time"

	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/gin-gonic/gin"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Session is one running porthole-* ephemeral container, surfaced by
// GET /debug/sessions for the topbar "where did I park my session"
// dropdown. The SPA uses (namespace, pod, ec) to jump back to the
// session without the user remembering where they injected it from.
type Session struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	EC        string `json:"ec"`
	Image     string `json:"image,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

// ListSessions returns every running porthole-injected ephemeral
// container the caller is allowed to see. "Allowed to see" is gated
// per namespace via OPA `list_pods`, with a one-call-per-namespace
// cache so a request that touches N pods across M namespaces costs M
// OPA round-trips, not N.
//
// Authorization shape:
//   - Top-level: list_namespaces (cluster-wide). If you can't browse
//     namespaces you can't see the session index at all.
//   - Per-entry: list_pods on the entry's namespace. Bindings
//     scoped to a namespace_glob filter the response so an
//     unprivileged user only sees their own session(s) in namespaces
//     they're already allowed to look at.
func ListSessions(c *gin.Context) {
	if !auth.AuthorizeOrAbort(c, auth.ActionListNamespaces, "", "") {
		return
	}
	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kubeconfig: " + err.Error()})
		return
	}
	pods, err := client.CoreV1().Pods("").List(c, v1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "list pods: " + err.Error()})
		return
	}

	// Per-namespace OPA cache scoped to this single request.
	allowed := map[string]bool{}
	out := make([]Session, 0, 16)
	// total counts every running porthole-* EC cluster-wide,
	// independent of OPA. It's surfaced as a small "discrete"
	// number next to the sessions dropdown so the user can see
	// how much porthole activity is happening overall, not just
	// inside the namespaces they personally can browse.
	total := 0

	for _, pod := range pods.Items {
		ns := pod.Namespace
		if _, seen := allowed[ns]; !seen {
			allowed[ns] = auth.Authorize(c, auth.ActionListPods, ns, "").Allow
		}

		// Image is on Spec.EphemeralContainers; the running marker
		// is on Status.EphemeralContainerStatuses. Build a small
		// lookup once per pod so the inner loop stays O(1).
		imageByName := make(map[string]string, len(pod.Spec.EphemeralContainers))
		for _, ec := range pod.Spec.EphemeralContainers {
			imageByName[ec.Name] = ec.Image
		}

		for _, st := range pod.Status.EphemeralContainerStatuses {
			if !strings.HasPrefix(st.Name, "porthole-") {
				continue
			}
			if st.State.Running == nil {
				continue
			}
			total++
			if !allowed[ns] {
				continue
			}
			s := Session{
				Namespace: ns,
				Pod:       pod.Name,
				EC:        st.Name,
				Image:     imageByName[st.Name],
			}
			if !st.State.Running.StartedAt.IsZero() {
				s.StartedAt = st.State.Running.StartedAt.UTC().Format(time.RFC3339)
			}
			out = append(out, s)
		}
	}

	c.JSON(http.StatusOK, gin.H{"sessions": out, "total": total})
}
