package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bcollard/porthole/pkg/authdata"
	"github.com/gin-gonic/gin"
)

// Actions Porthole asks OPA about. Keeping them as constants prevents
// silent drift between handler call sites and the Rego policy.
const (
	ActionListNamespaces = "list_namespaces"
	ActionListPods       = "list_pods"
	ActionListEC         = "list_ec"
	ActionInjectEC       = "inject_ec"
	ActionAttachEC       = "attach_ec"
	ActionTerminateEC    = "terminate_ec"
)

type opaClient struct {
	url    string
	client *http.Client
}

var (
	opaOnce sync.Once
	opa     *opaClient
)

func getOPA() *opaClient {
	opaOnce.Do(func() {
		opa = &opaClient{
			url:    os.Getenv("OPA_URL"),
			client: &http.Client{Timeout: 2 * time.Second},
		}
	})
	return opa
}

// What we actually send to OPA — explicitly *not* the raw token.
type opaUser struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// Namespace and Pod are emitted even when empty: the Rego policy keys
// off `ns == ""` to recognize cluster-wide actions, and omitempty
// would drop the field entirely, leaving Rego with an undefined
// value (which never compares equal to "").
type opaRequest struct {
	Action    string `json:"action"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
}

type opaInput struct {
	User            opaUser           `json:"user"`
	Request         opaRequest        `json:"request"`
	Now             string            `json:"now"`
	NamespaceLabels map[string]string `json:"namespace_labels"`
}

type opaResult struct {
	Result struct {
		Allow  bool   `json:"allow"`
		Reason string `json:"reason"`
	} `json:"result"`
}

// Decision is what Authorize returns. Reason is human-readable and is
// included in 403 responses so users (and audit logs) can see *why*.
type Decision struct {
	Allow  bool
	Reason string
}

// Authorize queries OPA for the given (action, namespace, pod). When
// OPA_URL is unset we allow with reason "opa disabled" so local dev
// doesn't need a sidecar.
func Authorize(c *gin.Context, action, namespace, pod string) Decision {
	o := getOPA()
	if o.url == "" {
		return Decision{Allow: true, Reason: "opa disabled"}
	}

	p, _ := PrincipalFromContext(c)
	user := opaUser{}
	if p != nil {
		user = opaUser{Sub: p.Sub, Email: p.Email, Groups: p.Groups}
	}

	body, _ := json.Marshal(map[string]any{
		"input": opaInput{
			User:            user,
			Request:         opaRequest{Action: action, Namespace: namespace, Pod: pod},
			Now:             time.Now().UTC().Format(time.RFC3339),
			NamespaceLabels: authdata.Default.Labels(c, namespace),
		},
	})

	req, err := http.NewRequestWithContext(c, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return Decision{Allow: false, Reason: "opa request build: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return Decision{Allow: false, Reason: "opa unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Decision{Allow: false, Reason: fmt.Sprintf("opa http %d", resp.StatusCode)}
	}
	var out opaResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Decision{Allow: false, Reason: "opa response unparseable: " + err.Error()}
	}
	return Decision{Allow: out.Result.Allow, Reason: out.Result.Reason}
}

// AuthorizeOrAbort is the helper handlers should call after parsing
// their input. On deny it writes 403 + JSON and aborts the gin chain.
func AuthorizeOrAbort(c *gin.Context, action, namespace, pod string) bool {
	d := Authorize(c, action, namespace, pod)
	if d.Allow {
		return true
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error":  "forbidden",
		"reason": d.Reason,
	})
	return false
}
