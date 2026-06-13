// Cleanup and sweep helpers for porthole-injected ephemeral
// containers.
//
// Ephemeral containers are immutable in the pod spec: once added,
// they remain there forever. "Cleanup" in this package therefore
// means *terminating the running process* inside an EC so the pod
// reclaims the resources. The spec entry stays in place (kube has
// no API to remove it), but kubelet flips the status from Running
// to Terminated.
//
// The mechanism is an exec into the EC's PID 1. We only ever touch
// ECs that carry the porthole prefix — anything else was created
// out-of-band and we leave it alone.

package ephemeral

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bcollard/porthole/pkg/kubeconfig"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog/v2"
)

// PortholeECPrefix is the name prefix Inject stamps onto every EC
// it creates ("porthole-<uuid8>"). Cleanup and Sweep filter on it
// so we never touch ECs another tool created.
const PortholeECPrefix = "porthole-"

// Terminated reports per-EC outcomes from Cleanup. OK=true means
// the kill signal was delivered successfully (the EC's exit may
// race with the response — that's expected).
type Terminated struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TerminateByName terminates a single porthole-injected ephemeral
// container by name. Refuses to touch ECs whose name lacks the
// porthole prefix — same defensive filter as Cleanup, so neither
// the UI nor a stray script can ask us to kill PID 1 in an EC we
// didn't create.
func TerminateByName(ctx context.Context, ns, pod, ec string) error {
	if !strings.HasPrefix(ec, PortholeECPrefix) {
		return fmt.Errorf("refusing to terminate non-porthole EC %q", ec)
	}
	return terminateOne(ctx, ns, pod, ec)
}

// Cleanup terminates every running porthole-injected ephemeral
// container in (ns, pod). Returns a per-EC report.
func Cleanup(ctx context.Context, ns, pod string) ([]Terminated, error) {
	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		return nil, fmt.Errorf("kube client: %w", err)
	}
	return cleanupWithClient(ctx, client, ns, pod)
}

func cleanupWithClient(ctx context.Context, client kubernetes.Interface, ns, pod string) ([]Terminated, error) {
	p, err := client.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s/%s: %w", ns, pod, err)
	}

	out := make([]Terminated, 0)
	for _, st := range p.Status.EphemeralContainerStatuses {
		if !strings.HasPrefix(st.Name, PortholeECPrefix) {
			continue
		}
		if st.State.Running == nil {
			continue
		}
		err := terminateOne(ctx, ns, pod, st.Name)
		t := Terminated{Name: st.Name, OK: err == nil}
		if err != nil {
			t.Error = err.Error()
		}
		out = append(out, t)
	}
	return out, nil
}

// terminateOne sends SIGHUP to PID 1 inside the named EC. Killing
// PID 1 makes the container exit, which closes the exec stream
// with a non-clean error — we tolerate the expected error shapes.
//
// Why SIGHUP and not SIGKILL/SIGTERM: with each EC in its own PID
// namespace (see list-create-ec.go), PID 1 is the EC's interactive
// shell (zsh for netshoot). Per pid_namespaces(7), signals sent to
// PID 1 from *inside* the namespace are only delivered when PID 1
// has a registered handler for them. SIGKILL and SIGSTOP can never
// have handlers (kernel-uncatchable), so the kernel silently drops
// them when delivered by a sibling — protection against the
// container accidentally suicide-ing itself. SIGTERM has no default
// handler in interactive zsh either, so it's dropped too.
//
// SIGHUP is the signal interactive shells install a handler for
// (terminal-hangup) and treat as "exit gracefully". It's what an
// SSH disconnect or detached tmux session sends. Empirically: SIGHUP
// inside the namespace tears down PID 1 immediately and the kernel
// then tears down the rest of the namespace; kubelet flips the EC
// to Terminated within ~1s.
func terminateOne(ctx context.Context, ns, pod, ec string) error {
	client, config, err := kubeconfig.GetKubClient()
	if err != nil {
		return fmt.Errorf("kube client: %w", err)
	}

	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: ec,
			Command:   []string{"sh", "-c", "kill -HUP 1"},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("exec init: %w", err)
	}
	var stdout, stderr bytes.Buffer
	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if streamErr != nil && !isExpectedExecExitErr(streamErr) {
		return fmt.Errorf("exec: %w (stderr=%q)", streamErr, stderr.String())
	}
	return nil
}

func isExpectedExecExitErr(err error) bool {
	s := err.Error()
	for _, m := range []string{"command terminated", "broken pipe", "EOF", "connection reset"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// SweepConfig controls the optional background sweeper.
type SweepConfig struct {
	// TTL: how long a running porthole EC may live before sweep
	// terminates it. Zero disables the sweeper.
	TTL time.Duration
	// Interval: scan cadence. Default = max(TTL/4, 1 minute).
	Interval time.Duration
}

// StartSweeper launches a background goroutine that periodically
// terminates porthole-injected ephemeral containers older than
// cfg.TTL. No-op when cfg.TTL is zero. Lifetime is bound to ctx.
//
// The sweeper lists pods cluster-wide on every tick. For small
// clusters or low TTLs that's fine; for large clusters set a
// generous TTL (e.g. 30m) so the scan cost amortizes.
func StartSweeper(ctx context.Context, cfg SweepConfig) {
	if cfg.TTL <= 0 {
		return
	}
	if cfg.Interval <= 0 {
		cfg.Interval = max(cfg.TTL/4, time.Minute)
	}
	go sweepLoop(ctx, cfg)
	klog.Infof("EC sweeper: TTL=%s interval=%s", cfg.TTL, cfg.Interval)
}

func sweepLoop(ctx context.Context, cfg SweepConfig) {
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			sweepOnce(ctx, cfg.TTL)
		}
	}
}

func sweepOnce(ctx context.Context, ttl time.Duration) {
	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		klog.Warningf("sweeper: kube client: %v", err)
		return
	}
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Warningf("sweeper: list pods: %v", err)
		return
	}
	cutoff := time.Now().Add(-ttl)
	for _, p := range pods.Items {
		for _, st := range p.Status.EphemeralContainerStatuses {
			if !strings.HasPrefix(st.Name, PortholeECPrefix) {
				continue
			}
			if st.State.Running == nil {
				continue
			}
			if st.State.Running.StartedAt.Time.After(cutoff) {
				continue
			}
			if err := terminateOne(ctx, p.Namespace, p.Name, st.Name); err != nil {
				klog.Warningf("sweeper: terminate %s/%s/%s: %v", p.Namespace, p.Name, st.Name, err)
				continue
			}
			klog.Infof("sweeper: terminated %s/%s/%s (age > %s)", p.Namespace, p.Name, st.Name, ttl)
		}
	}
}
