package ephemeral

import (
	"bufio"
	"context"
	"fmt"
	"github.com/bcollard/porthole/pkg/kubeconfig"
	"github.com/bcollard/porthole/pkg/util"
	"github.com/gin-gonic/gin"
	"io"
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
	"net/url"
	"time"
)

func Attach(ctx *gin.Context, ns string, podName string, debuggerName string, streamz util.Streamz, tty bool) {
	client, config, err := kubeconfig.GetKubClient()
	if err != nil {
		fmt.Errorf("error getting Kubernetes client: %v", err)
	}

	fmt.Printf("Waiting for debugger container...\n")
	pod, err := waitForContainer(ctx, client, ns, podName, debuggerName, true)
	if err != nil {
		panic(err)
	}

	debuggerContainer := ephemeralContainerByName(pod, debuggerName)
	if debuggerContainer == nil {
		fmt.Errorf("cannot find debugger container %q in pod %q", debuggerName, podName)
		panic(err)
	}

	fmt.Printf("Attaching to debugger container...\n")
	fmt.Printf("If you don't see a command prompt, try pressing enter.\n")
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
		// Debugger container is not running anymore - streaming no longer needed.
		cancelStreamingCtx()
	}()

	if err := stream(streamingCtx, streamz, req.URL(), config, tty); err != nil {
		fmt.Printf("error streaming to/from debugger container: %v", err)
	}

	if err := dumpDebuggerLogs(ctx, client, ns, podName, debuggerName, ctx.Writer); err != nil {
		fmt.Printf("error dumping debugger logs: %v", err)
	}
}

func stream(
	ctx context.Context,
	streamz util.Streamz,
	url *url.URL,
	config *restclient.Config,
	raw bool,
) error {
	//var resizeQueue *tty.ResizeQueue
	//if raw {
	//	if cli.OutputStream().IsTerminal() {
	//		resizeQueue = tty.NewResizeQueue(ctx, cli.OutputStream())
	//		resizeQueue.Start()
	//	}
	//
	//	cli.InputStream().SetRawTerminal()
	//	cli.OutputStream().SetRawTerminal()
	//	defer func() {
	//		cli.InputStream().RestoreTerminal()
	//		cli.OutputStream().RestoreTerminal()
	//	}()
	//}

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
		Stdin:  streamz.Input,
		Stdout: streamz.Output,
		Stderr: streamz.Error,
		Tty:    raw,
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

func dumpDebuggerLogs(
	ctx context.Context,
	client kubernetes.Interface,
	ns string,
	podName string,
	containerName string,
	out io.Writer,
) error {
	fmt.Printf("Dumping logs for %s/%s...\n", ns, podName)
	req := client.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		Follow:    false,
	})

	fmt.Printf("Streaming logs...\n")
	readCloser, err := req.Stream(ctx)
	if err != nil {
		return err
	}
	defer readCloser.Close()

	fmt.Printf("Writing logs...\n")
	r := bufio.NewReader(readCloser)
	for {
		bytes, err := r.ReadBytes('\n')
		if _, err := out.Write(bytes); err != nil {
			return err
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
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

func Exec(ctx *gin.Context, ns string, podName string, debuggerName string, streamz util.Streamz, tty bool, message []byte) {
	client, config, err := kubeconfig.GetKubClient()
	if err != nil {
		fmt.Errorf("error getting Kubernetes client: %v", err)
	}

	fmt.Printf("Waiting for debugger container...\n")
	pod, err := waitForContainer(ctx, client, ns, podName, debuggerName, true)
	if err != nil {
		panic(err)
	}

	debuggerContainer := ephemeralContainerByName(pod, debuggerName)
	if debuggerContainer == nil {
		fmt.Errorf("cannot find debugger container %q in pod %q", debuggerName, podName)
		panic(err)
	}

	fmt.Printf("Attaching to debugger container...\n")
	fmt.Printf("If you don't see a command prompt, try pressing enter.\n")
	req := client.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debuggerName,
			Command:   []string{string(message)},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	streamingCtx, cancelStreamingCtx := context.WithCancel(ctx)
	defer cancelStreamingCtx()

	// if container dies, stop streaming
	go func() {
		_, _ = waitForContainer(ctx, client, ns, podName, debuggerName, false)
		// Debugger container is not running anymore - streaming no longer needed.
		cancelStreamingCtx()
	}()

	if err := stream(streamingCtx, streamz, req.URL(), config, tty); err != nil {
		fmt.Printf("error streaming to/from debugger container: %v", err)
	}

	if err := dumpDebuggerLogs(ctx, client, ns, podName, debuggerName, ctx.Writer); err != nil {
		fmt.Printf("error dumping debugger logs: %v", err)
	}
}
