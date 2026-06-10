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
// onto the context. The SPA uses it to render the connected user in
// the topbar. Mounted under the protected route group so the JWT has
// already been parsed by the time we reach this handler.
func GetMe(c *gin.Context) {
	p, ok := auth.PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no principal"})
		return
	}
	c.JSON(http.StatusOK, p)
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
