package ephemeral

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/bcollard/porthole/pkg/util"
	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"
	watchtools "k8s.io/client-go/tools/watch"
)

// Attach attaches to an ephemeral debugger container's TTY. The
// streamz carries stdin/stdout/stderr, and resize (optional) receives
// terminal-size updates from the client. If resize is nil, the PTY
// stays at whatever size the kubelet defaults to.
func Attach(
	ctx *gin.Context,
	ns string,
	podName string,
	debuggerName string,
	streamz util.Streamz,
	resize <-chan util.TerminalSize,
	tty bool,
) {
	client, config, err := kubeconfig.GetKubClient()
	if err != nil {
		fmt.Printf("error getting Kubernetes client: %v\n", err)
		return
	}

	fmt.Printf("Waiting for debugger container...\n")
	pod, err := waitForContainer(ctx, client, ns, podName, debuggerName, true)
	if err != nil {
		fmt.Printf("error waiting for debugger container: %v\n", err)
		return
	}

	debuggerContainer := ephemeralContainerByName(pod, debuggerName)
	if debuggerContainer == nil {
		fmt.Printf("cannot find debugger container %q in pod %q\n", debuggerName, podName)
		return
	}

	fmt.Printf("Attaching to debugger container...\n")
	req := client.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: debuggerName,
			Stdin:     true,
			Stdout:    true,
			Stderr:    false,
			TTY:       true,
		}, scheme.ParameterCodec)

	streamingCtx, cancelStreamingCtx := context.WithCancel(ctx)
	defer cancelStreamingCtx()

	// if container dies, stop streaming
	go func() {
		_, _ = waitForContainer(ctx, client, ns, podName, debuggerName, false)
		cancelStreamingCtx()
	}()

	var queue remotecommand.TerminalSizeQueue
	if resize != nil {
		queue = &chanSizeQueue{ch: resize}
	}

	if err := stream(streamingCtx, streamz, req.URL(), config, tty, queue); err != nil {
		fmt.Printf("error streaming to/from debugger container: %v\n", err)
	}
}

// chanSizeQueue adapts a <-chan util.TerminalSize to
// remotecommand.TerminalSizeQueue. Next returns nil when the channel
// closes, signalling the executor to stop polling.
type chanSizeQueue struct {
	ch <-chan util.TerminalSize
}

func (q *chanSizeQueue) Next() *remotecommand.TerminalSize {
	sz, ok := <-q.ch
	if !ok {
		return nil
	}
	return &remotecommand.TerminalSize{Width: sz.Cols, Height: sz.Rows}
}

func stream(
	ctx context.Context,
	streamz util.Streamz,
	url *url.URL,
	config *restclient.Config,
	raw bool,
	sizeQueue remotecommand.TerminalSizeQueue,
) error {
	fmt.Printf("Creating executors for url: %s...\n", url.String())

	spdyExec, err := remotecommand.NewSPDYExecutor(config, "POST", url)
	if err != nil {
		return fmt.Errorf("cannot create SPDY executor: %w", err)
	}

	websocketExec, err := remotecommand.NewWebSocketExecutor(config, "GET", url.String())
	if err != nil {
		return fmt.Errorf("cannot create WebSocket executor: %w", err)
	}

	exec, err := remotecommand.NewFallbackExecutor(websocketExec, spdyExec, httpstream.IsUpgradeFailure)
	if err != nil {
		return fmt.Errorf("cannot create fallback executor: %w", err)
	}

	fmt.Printf("Streaming to %s...\n", url.String())
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             streamz.Input,
		Stdout:            streamz.Output,
		Stderr:            streamz.Error,
		Tty:               raw,
		TerminalSizeQueue: sizeQueue,
	})
}

func waitForContainer(
	ctx context.Context,
	client kubernetes.Interface,
	ns string,
	podName string,
	containerName string,
	running bool,
) (*corev1.Pod, error) {
	ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, 0*time.Second)
	defer cancel()

	fieldSelector := fields.OneTermEqualSelector("metadata.name", podName).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return client.CoreV1().Pods(ns).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return client.CoreV1().Pods(ns).Watch(ctx, options)
		},
	}

	ev, err := watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, func(ev watch.Event) (bool, error) {
		switch ev.Type {
		case watch.Deleted:
			return false, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "")
		}

		p, ok := ev.Object.(*corev1.Pod)
		if !ok {
			return false, fmt.Errorf("watch did not return a pod: %v", ev.Object)
		}

		s := containerStatusByName(p, containerName)
		if s == nil {
			return false, nil
		}

		if s.LastTerminationState.Terminated != nil || s.State.Terminated != nil || (running && s.State.Running != nil) {
			return true, nil
		}

		return false, nil
	})
	if ev != nil {
		return ev.Object.(*corev1.Pod), err
	}

	return nil, err
}

func containerStatusByName(pod *corev1.Pod, containerName string) *corev1.ContainerStatus {
	allContainerStatus := [][]corev1.ContainerStatus{
		pod.Status.InitContainerStatuses,
		pod.Status.ContainerStatuses,
		pod.Status.EphemeralContainerStatuses,
	}
	for _, statuses := range allContainerStatus {
		for i := range statuses {
			if statuses[i].Name == containerName {
				return &statuses[i]
			}
		}
	}
	return nil
}

func ephemeralContainerByName(pod *corev1.Pod, containerName string) *corev1.EphemeralContainer {
	for i := range pod.Spec.EphemeralContainers {
		if pod.Spec.EphemeralContainers[i].Name == containerName {
			return &pod.Spec.EphemeralContainers[i]
		}
	}
	return nil
}

