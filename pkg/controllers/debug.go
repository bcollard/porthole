package controllers

import (
	"fmt"
	"github.com/bcollard/porthole/pkg/ephemeral"
	"github.com/gin-gonic/gin"
)

type InjectPayload struct {
	Namespace string `json:"namespace" binding:"required"`
	Pod       string `json:"pod" binding:"required"`
	Image     string `json:"image"`
	Command   string `json:"command"`
}

type ExecPayload struct {
	Namespace          string `json:"namespace" binding:"required"`
	Pod                string `json:"pod" binding:"required"`
	Command            string `json:"command" binding:"required"`
	tty                bool   `json:"tty"`
	ephemeralContainer string `json:"ephemeralContainer" binding:"required"`
}

type PodNamespacePayload struct {
	Namespace string `json:"namespace" binding:"required"`
	Pod       string `json:"pod" binding:"required"`
}

var image string = "pileenretard/busybox:1.2"

func Inject(context *gin.Context) {
	var payload InjectPayload
	err := context.BindJSON(&payload)
	if err != nil {
		fmt.Errorf("error binding JSON: %v", err)
		context.JSON(400, gin.H{
			"message": "Invalid JSON",
		})
	}
	if payload.Image == "" {
		payload.Image = image
	}

	debugCtrName := ephemeral.Inject(context, payload.Namespace, payload.Pod, payload.Image, payload.Command)

	context.JSON(200, gin.H{
		"ns/pod":             payload.Namespace + "/" + payload.Pod,
		"debugContainerName": debugCtrName,
	})

}

func Exec(context *gin.Context) {
	context.JSON(200, gin.H{
		"message": "Exec",
	})
}

func List(context *gin.Context) {
	var payload PodNamespacePayload
	err := context.BindJSON(&payload)
	if err != nil {
		fmt.Errorf("error binding JSON: %v", err)
		context.JSON(400, gin.H{
			"message": "Invalid JSON",
		})
	}

	ecs := listEphemeralContainersForPod(context, payload.Namespace, payload.Pod)

	context.JSON(200, gin.H{
		"ns/pod":              payload.Namespace + "/" + payload.Pod,
		"ephemeralContainers": ecs,
	})
}

func listEphemeralContainersForPod(context *gin.Context, ns string, pod string) []ephemeral.EphemeralContainer {
	return ephemeral.List(context, ns, pod)
}
