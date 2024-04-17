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
	"text/template"
)

type EphemeralContainer struct {
	Name    string
	Command []string
}

var (
	simpleEntrypoint = template.Must(template.New("user-entrypoint").Parse(`
set -eu

export CDEBUG_ROOTFS=/

if [ "${HOME:-/}" != "/" ]; then
	ln -s /proc/{{ .TARGET_PID }}/root/ ${HOME}target-rootfs
fi

# TODO: Add target container's PATH to the user's PATH

exec {{ .Cmd }}
`))
)

// list ephemeral containers
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
		fmt.Println("Pod spec has ephemeral containers defined.")
		for _, container := range pod.Spec.EphemeralContainers {
			ephemeralContainerList = append(ephemeralContainerList, EphemeralContainer{Name: container.Name, Command: container.Command})
		}
	} else {
		fmt.Println("Pod spec does not have ephemeral containers defined.")
	}

	return ephemeralContainerList
}

func Inject(context *gin.Context, namespace string, pod string, image string, command string) {
	kubeClient, _ := kubeconfig.GetKubClient()
	// get the pod
	podObj, err := kubeClient.CoreV1().Pods(namespace).Get(context, pod, metav1.GetOptions{})
	if err != nil {
		panic(err)
	}

	// generate a short UUID
	id := uuid.New().String()[:8]

	// create the ephemeral container
	ec := &corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:                     "porthole-" + id,
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

}

func ClearEphemeralContainers(context *gin.Context, namespace string, pod string) {
	kubeClient, _ := kubeconfig.GetKubClient()
	// get the pod
	podObj, err := kubeClient.CoreV1().Pods(namespace).Get(context, pod, metav1.GetOptions{})
	if err != nil {
		fmt.Errorf("error getting pod: %v", err)
	}

	// Check for ephemeral containers in the pod spec
	if len(podObj.Spec.EphemeralContainers) > 0 {
		fmt.Println("Pod spec has ephemeral containers defined. Clearing them...")

		// collect the names of the ephemeral containers with status "running"
		var ephemeralContainerNames []string
		for _, container := range podObj.Status.EphemeralContainerStatuses {
			if container.State.Running != nil {
				ephemeralContainerNames = append(ephemeralContainerNames, container.Name)
			}
		}

		fmt.Println("Ephemeral containers to be cleared: ", ephemeralContainerNames)

		copied := podObj.DeepCopy()

		// for each ephemeral container to be cleared, set the command to exit
		for _, containerName := range ephemeralContainerNames {
			for i, container := range copied.Spec.EphemeralContainers {
				if container.Name == containerName {
					copied.Spec.EphemeralContainers[i].Command = []string{"echo dyiing...", "kill 1"}
				}
			}
		}

		podJSON, err := json.Marshal(podObj)
		if err != nil {
			panic(err.Error())
		}

		podWithEphemeralContainerJSON, err := json.Marshal(copied)
		if err != nil {
			panic(err.Error())
		}
		fmt.Println("New Pod JSON: ", string(podWithEphemeralContainerJSON))

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
	} else {
		fmt.Println("Pod spec does not have ephemeral containers defined.")
		return
	}

	fmt.Printf("Pod has %d ephemeral containers.\n", len(podObj.Spec.EphemeralContainers))

}
