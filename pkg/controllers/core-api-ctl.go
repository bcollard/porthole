package controllers

import (
	"fmt"
	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
)

// getAlbums responds with the list of all albums as JSON.
func GetNamespaces(c *gin.Context) {
	client, err := kubeconfig.GetKubClient()
	if err != nil {
		fmt.Errorf("error getting Kubernetes client: %v", err)
	}

	// list the namespaces
	namespaces, _ := client.CoreV1().Namespaces().List(c, v1.ListOptions{})
	nsList := []string{}
	for _, ns := range namespaces.Items {
		nsList = append(nsList, ns.ObjectMeta.Name)
	}

	c.IndentedJSON(http.StatusOK, nsList)
}

func GetPods(c *gin.Context) {
	namespace := c.Param("ns")

	client, err := kubeconfig.GetKubClient()
	if err != nil {
		fmt.Errorf("error getting Kubernetes client: %v", err)
	}

	pods, _ := client.CoreV1().Pods(namespace).List(c, v1.ListOptions{})
	podList := []string{}
	for _, pod := range pods.Items {
		podList = append(podList, pod.ObjectMeta.Name)
	}

	c.IndentedJSON(http.StatusOK, podList)
}
