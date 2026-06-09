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
	Name       string
	Command    []string
	Running    bool
	Terminated bool
}

func List(c *gin.Context, namespace string, podName string) ([]EphemeralContainer, error) {
	kubeClient, _, err := kubeconfig.GetKubClient()
	if err != nil {
		return nil, fmt.Errorf("kube client: %w", err)
	}

	pod, err := kubeClient.CoreV1().Pods(namespace).Get(c, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s/%s: %w", namespace, podName, err)
	}

	out := make([]EphemeralContainer, 0, len(pod.Spec.EphemeralContainers))
	for _, container := range pod.Spec.EphemeralContainers {
		var running, terminated bool
		for _, st := range pod.Status.EphemeralContainerStatuses {
			if st.Name != container.Name {
				continue
			}
			if st.State.Running != nil {
				running = true
			}
			if st.State.Terminated != nil {
				terminated = true
			}
			break
		}
		out = append(out, EphemeralContainer{
			Name:       container.Name,
			Command:    container.Command,
			Running:    running,
			Terminated: terminated,
		})
	}
	return out, nil
}

func Inject(c *gin.Context, namespace, pod, image, command string) (string, error) {
	if namespace == "" || pod == "" || image == "" {
		return "", fmt.Errorf("namespace, pod and image are required")
	}

	kubeClient, _, err := kubeconfig.GetKubClient()
	if err != nil {
		return "", fmt.Errorf("kube client: %w", err)
	}

	podObj, err := kubeClient.CoreV1().Pods(namespace).Get(c, pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %s/%s: %w", namespace, pod, err)
	}
	if len(podObj.Spec.Containers) == 0 {
		return "", fmt.Errorf("pod %s/%s has no containers to target", namespace, pod)
	}

	debugCtrName := "porthole-" + uuid.New().String()[:8]

	ec := corev1.EphemeralContainer{
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
	if command != "" {
		ec.Command = []string{"sh", "-c", command}
	}

	copied := podObj.DeepCopy()
	copied.Spec.EphemeralContainers = append(copied.Spec.EphemeralContainers, ec)

	podJSON, err := json.Marshal(podObj)
	if err != nil {
		return "", fmt.Errorf("marshal pod: %w", err)
	}
	patchedJSON, err := json.Marshal(copied)
	if err != nil {
		return "", fmt.Errorf("marshal patched pod: %w", err)
	}
	patch, err := strategicpatch.CreateTwoWayMergePatch(podJSON, patchedJSON, podObj)
	if err != nil {
		return "", fmt.Errorf("compute patch: %w", err)
	}

	if _, err = kubeClient.CoreV1().Pods(namespace).Patch(
		c,
		pod,
		types.StrategicMergePatchType,
		patch,
		metav1.PatchOptions{},
		"ephemeralcontainers",
	); err != nil {
		return "", fmt.Errorf("patch ephemeralcontainers: %w", err)
	}

	return debugCtrName, nil
}
