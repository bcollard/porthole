// Package authdata pulls k8s-side facts (currently: namespace labels)
// that the OPA policy needs as input. Lookups are cached with a short
// TTL so a busy request path doesn't generate one kube API call per
// authZ check.
package authdata

import (
	"context"
	"sync"
	"time"

	"github.com/bcollard/porthole/pkg/kubeconfig"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type nsEntry struct {
	labels  map[string]string
	expires time.Time
}

// NamespaceLabelCache caches per-namespace labels with a fixed TTL.
// The cache fails open: a lookup error returns an empty map rather
// than blocking the request — the deny vs allow decision still rests
// with OPA, and a labels-required binding simply won't match.
type NamespaceLabelCache struct {
	TTL time.Duration

	mu      sync.RWMutex
	entries map[string]nsEntry
}

// Default is the shared cache used by pkg/auth.
var Default = &NamespaceLabelCache{
	TTL:     60 * time.Second,
	entries: map[string]nsEntry{},
}

// Labels returns the labels of ns. Empty ns returns an empty map.
// Errors return an empty map (see fail-open note above).
func (c *NamespaceLabelCache) Labels(ctx context.Context, ns string) map[string]string {
	if ns == "" {
		return map[string]string{}
	}

	now := time.Now()
	c.mu.RLock()
	if e, ok := c.entries[ns]; ok && e.expires.After(now) {
		c.mu.RUnlock()
		return e.labels
	}
	c.mu.RUnlock()

	client, _, err := kubeconfig.GetKubClient()
	if err != nil {
		return map[string]string{}
	}
	nsObj, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		return map[string]string{}
	}
	labels := nsObj.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	c.mu.Lock()
	c.entries[ns] = nsEntry{labels: labels, expires: now.Add(c.TTL)}
	c.mu.Unlock()
	return labels
}
