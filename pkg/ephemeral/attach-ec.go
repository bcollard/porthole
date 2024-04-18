package ephemeral

import (
	"fmt"
	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/gin-gonic/gin"
)

func Attach(ctx *gin.Context, ns string, pod string, container string) {
	client, err := kubeconfig.GetKubClient()
	if err != nil {
		fmt.Errorf("error getting Kubernetes client: %v", err)
	}

	result := client.RESTClient().Post().Namespace(ns).Resource("pods").Name(pod).SubResource("attach").Param("container", container).Do(ctx)
	if result.Error() != nil {
		fmt.Errorf("error attaching to pod: %v", result.Error())
	}

	bytes, err := result.Raw()
	if err != nil {
		fmt.Errorf("error reading response: %v", err)
	}

	ctx.Data(200, "application/json", bytes)

}
