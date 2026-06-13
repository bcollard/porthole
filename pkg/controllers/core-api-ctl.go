package controllers

import (
	"net/http"
	"strings"

	"github.com/bcollard/porthole/pkg/auth"
	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/gin-gonic/gin"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetNamespaces(c *gin.Context) {
	if !auth.AuthorizeOrAbort(c, auth.ActionListNamespaces, "", "") {
		return
	}
	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kubeconfig: " + err.Error()})
		return
	}

	namespaces, err := client.CoreV1().Namespaces().List(c, v1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "list namespaces: " + err.Error()})
		return
	}

	nsList := make([]string, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		nsList = append(nsList, ns.ObjectMeta.Name)
	}
	c.IndentedJSON(http.StatusOK, nsList)
}

// GetMe returns the validated Principal the JWT middleware stamped
// onto the context, plus the OPA bindings that match the user's
// groups. The SPA uses it to render the connected user *and* their
// role chips in the topbar. Mounted under the protected route group
// so the JWT has already been parsed by the time we reach this
// handler. `bindings` is omitted when OPA is disabled or unreachable.
func GetMe(c *gin.Context) {
	p, ok := auth.PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no principal"})
		return
	}
	out := gin.H{
		"sub":                p.Sub,
		"email":              p.Email,
		"groups":             p.Groups,
		"preferred_username": p.PreferredUsername,
		"name":               p.Name,
		"given_name":         p.GivenName,
		"family_name":        p.FamilyName,
		"azp":                p.AuthorizedParty,
	}
	if bindings := auth.EffectiveBindings(c); bindings != nil {
		out["bindings"] = bindings
	}
	c.JSON(http.StatusOK, out)
}

func GetPods(c *gin.Context) {
	namespace := c.Param("ns")
	if !auth.AuthorizeOrAbort(c, auth.ActionListPods, namespace, "") {
		return
	}

	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kubeconfig: " + err.Error()})
		return
	}

	pods, err := client.CoreV1().Pods(namespace).List(c, v1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "list pods: " + err.Error()})
		return
	}

	podList := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		podList = append(podList, pod.ObjectMeta.Name)
	}
	c.IndentedJSON(http.StatusOK, podList)
}

// GetServices lists services in a namespace and the ports they
// expose. The SPA renders the result in the "Service Viewer" panel
// so an operator inside a debug container can see what's curl-able
// next door. Reuses the list_pods OPA action: if you can enumerate
// pods in the namespace you can already infer what services exist
// from their selectors, so a separate action would be theatre.
func GetServices(c *gin.Context) {
	ns := c.Param("ns")
	if !auth.AuthorizeOrAbort(c, auth.ActionListPods, ns, "") {
		return
	}

	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kubeconfig: " + err.Error()})
		return
	}

	svcs, err := client.CoreV1().Services(ns).List(c, v1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "list services: " + err.Error()})
		return
	}

	type port struct {
		Name     string `json:"name,omitempty"`
		Port     int32  `json:"port"`
		Protocol string `json:"protocol,omitempty"`
	}
	type svc struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Ports []port `json:"ports"`
	}
	out := make([]svc, 0, len(svcs.Items))
	for _, s := range svcs.Items {
		ports := make([]port, 0, len(s.Spec.Ports))
		for _, p := range s.Spec.Ports {
			ports = append(ports, port{
				Name:     p.Name,
				Port:     p.Port,
				Protocol: string(p.Protocol),
			})
		}
		out = append(out, svc{
			Name:  s.ObjectMeta.Name,
			Type:  string(s.Spec.Type),
			Ports: ports,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"namespace": ns,
		"services":  out,
	})
}

// GetPod returns metadata for a single pod — currently just labels.
// The SPA shows these under the Target header so the operator has
// context (team, tier, app) before they inject a debug container.
// Same OPA action as the list — if you can list pods in this ns
// you can see one pod's labels.
func GetPod(c *gin.Context) {
	ns := c.Param("ns")
	name := c.Param("pod")
	if !auth.AuthorizeOrAbort(c, auth.ActionListPods, ns, name) {
		return
	}

	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kubeconfig: " + err.Error()})
		return
	}

	pod, err := client.CoreV1().Pods(ns).Get(c, name, v1.GetOptions{})
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "get pod: " + err.Error()})
		return
	}

	labels := pod.ObjectMeta.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"name":   pod.ObjectMeta.Name,
		"labels": labels,
	})
}
