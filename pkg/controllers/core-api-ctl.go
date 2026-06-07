package controllers

import (
	"net/http"

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
