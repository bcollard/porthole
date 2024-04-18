package ephemeral

import (
	"encoding/json"
	"fmt"
	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

type EphemeralContainer struct {
	Name    string
	Command []string
	Running bool
}

func List(c *gin.Context, namespace string, podName string) []EphemeralContainer {
	kubeClient, _ := kubeconfig.GetKubClient()
	// list the ephemeral containers for the pod
	var ephemeralContainerList []EphemeralContainer

	pod, err := kubeClient.CoreV1().Pods(namespace).Get(c, podName, metav1.GetOptions{})
	if err != nil {
		panic(err)
	}

	// Check for ephemeral containers in the pod spec
	if len(pod.Spec.EphemeralContainers) > 0 {
		for _, container := range pod.Spec.EphemeralContainers {
			var running bool
			for _, ctrStatus := range pod.Status.EphemeralContainerStatuses {
				if ctrStatus.Name == container.Name {
					if ctrStatus.State.Running != nil {
						running = true
					}
				}
			}
			ephemeralContainerList = append(ephemeralContainerList, EphemeralContainer{
				Name:    container.Name,
				Command: container.Command,
				Running: running,
			})
		}
	} else {
		fmt.Println("Pod spec does not have ephemeral containers defined.")
	}

	return ephemeralContainerList
}

func Inject(context *gin.Context, namespace string, pod string, image string, command string) string {
	kubeClient, _ := kubeconfig.GetKubClient()
	// get the pod
	podObj, err := kubeClient.CoreV1().Pods(namespace).Get(context, pod, metav1.GetOptions{})
	if err != nil {
		panic(err)
	}

	// generate a short UUID
	debugCtrName := "porthole-" + uuid.New().String()[:8]

	// create the ephemeral container
	ec := &corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:                     debugCtrName,
			Image:                    image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Stdin:                    true,
			TTY:                      true,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		},
		TargetContainerName: podObj.Spec.Containers[0].Name,
	}

	// add the command if it was provided
	if command != "" {
		ec.Command = []string{"sh", "-c", "exec", command}
	}

	copied := podObj.DeepCopy()
	copied.Spec.EphemeralContainers = append(copied.Spec.EphemeralContainers, *ec)

	podJSON, err := json.Marshal(podObj)
	if err != nil {
		panic(err.Error())
	}

	podWithEphemeralContainerJSON, err := json.Marshal(copied)
	if err != nil {
		panic(err.Error())
	}

	patch, err := strategicpatch.CreateTwoWayMergePatch(podJSON, podWithEphemeralContainerJSON, podObj)
	if err != nil {
		panic(err.Error())
	}

	podObj, err = kubeClient.CoreV1().
		Pods(podObj.Namespace).
		Patch(
			context,
			podObj.Name,
			types.StrategicMergePatchType,
			patch,
			metav1.PatchOptions{},
			"ephemeralcontainers",
		)
	if err != nil {
		panic(err.Error())
	}

	fmt.Printf("Pod has %d ephemeral containers.\n", len(podObj.Spec.EphemeralContainers))

	return debugCtrName
}
