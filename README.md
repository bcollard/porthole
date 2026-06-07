# Porthole

Web-based debug terminal for Kubernetes — pick a pod, inject an ephemeral container with the image you want (`busybox`, `netshoot`, `psql`, …), attach to it from the browser. Pluggable authN/authZ so developers reach pods without `kubectl`, and without the cluster having to know their corporate identity.

## Big picture

Three diagrams, each at a different zoom level:

- **[`docs/architecture.svg`](./docs/architecture.svg)** — system layout: browser → Envoy Gateway (+ OIDC) → Porthole → kube-apiserver → kubelet → ephemeral container.
- **[`docs/traffic-flow.svg`](./docs/traffic-flow.svg)** — byte paths through an attach session: stdout, stdin, and resize travel three distinct chains across the websocket, the k8s executor, the kubelet, and the PTY.
- **[`docs/sequence.svg`](./docs/sequence.svg)** — page load → discovery → inject → attach → live session → close.

## Why

- **Simplicity.** Developers connect to a backend app in the cluster instead of proxying `kubectl exec` from their laptop. Lens/k9s also work but make the OIDC integration awkward when teams use different IdPs from the cluster.
- **Flexibility.** Inject *any* image you can pull from a registry as the debug container — `psql`, `redis-cli`, `dig`, your own forensic tools. No special configuration on the target pod.
- **Zero-trust.** The cluster's mesh policies still apply to the ephemeral container: it only reaches what the target pod can reach.
- **Corporate-identity friendly.** Authentication is enforced at the API gateway (Envoy Gateway + OIDC). The cluster doesn't need to know your corporate users; Porthole reads identity claims out of the JWT and consults an OPA sidecar for authorization.

## Repo layout

```
.
├── main.go                              # single-port gin engine
├── pkg/
│   ├── controllers/                     # HTTP/WS handlers (discovery, inject, attach)
│   ├── ephemeral/                       # k8s patch + attach via remotecommand
│   ├── util/                            # WsSession (binary stdin + JSON-text control)
│   ├── auth/                            # JWT middleware + OPA client
│   ├── authdata/                        # cached ns-label lookups for OPA input
│   ├── audit/                           # one slog JSON line per inject/attach decision
│   ├── kubeconfig/                      # in-cluster + ~/.kube fallback
│   └── web/dist/                        # embedded SPA (xterm.js, vanilla ES modules)
├── policy/
│   ├── porthole.rego                    # authZ rules — groups × namespace × labels × time
│   └── data.json                        # roles + bindings
├── deploy/
│   ├── deployment-ko.yaml               # porthole + opa sidecar
│   ├── rbac.yaml                        # ClusterRole for pods/ephemeralcontainers
│   ├── opa/policy-configmap.yaml        # rego + data mounted into the sidecar
│   └── envoy-gateway/                   # Gateway, HTTPRoute, SecurityPolicy (OIDC)
├── scripts/
│   ├── runlocal-subdomain-bootstrap.sh  # bco.runlocal.dev DNS + wildcard cert (lego)
│   ├── keycloak-bootstrap.sh            # realm/client/user via kc CLI
│   ├── envoy-smoke.sh                   # ROPC + curl through the gateway
│   └── opa-eval.sh                      # 15 policy cases run locally
├── docs/                                # the SVGs above
└── Makefile
```

## Quickstart — local, no cluster, no auth

Bare metal — just talks to whatever cluster your `kubectl` currently points at.

```sh
AUTH_DISABLED=true make run-local
open http://localhost:8081/ui/
```

This stamps a `local-dev` principal onto every request and skips OPA, so you can drive the UI immediately. Audit logs still emit (with `user:"local-dev"`).

## Deploy on a fresh klimax kind cluster

End-to-end happy path:

```sh
make cluster-up           # klimax cluster create porthole-e2e
make runlocal-bootstrap   # one-time: DNS zone + wildcard TLS cert for bco.runlocal.dev
make envoy-install        # helm install envoy-gateway
make deploy               # ko build + RBAC + Deployment + Service + OPA ConfigMap
make keycloak-bootstrap   # creates realm/client/user in the managed Keycloak
#   ... copy the printed client_secret into deploy/envoy-gateway/secret.yaml ...
kubectl apply -f deploy/envoy-gateway/secret.yaml
make gateway-apply        # Gateway + HTTPRoute + SecurityPolicy
open https://porthole.bco.runlocal.dev/ui/
```

`make e2e` chains the same sequence and prints the manual steps that remain.

## Authentication (Envoy Gateway + Keycloak)

Envoy Gateway terminates TLS, runs the OIDC handshake against Keycloak, and forwards authenticated requests to Porthole. Porthole validates the JWT itself (so it never trusts a header it can't verify):

| Env var          | Example                                                                        |
|------------------|--------------------------------------------------------------------------------|
| `JWKS_URL`       | `https://keycloak.kong.runlocal.dev/realms/porthole/protocol/openid-connect/certs` |
| `OIDC_ISSUER`    | `https://keycloak.kong.runlocal.dev/realms/porthole`                           |
| `OIDC_AUDIENCE`  | _(optional)_ expected `aud` claim                                              |
| `AUTH_DISABLED`  | `true` to bypass JWT validation entirely (local dev)                            |

The middleware reads the token from `X-ID-Token` first, then falls back to `Authorization: Bearer …`.

> **Note on `forwardIDToken`.** The intent is for Envoy to forward the OIDC `id_token` as a custom header. The `SecurityPolicy.oidc.forwardIDToken` field exists in the EG v1alpha1 API but is marked `+notImplementedHide` in v1.6, so today the SecurityPolicy uses `forwardAccessToken: true` (sends `Authorization: Bearer <access_token>`). The Keycloak access_token carries the same identity claims when the client maps `openid+profile+email` scopes, so authZ semantics are unchanged. The migration is a 2-line swap when EG ships the implementation.

## Authorization (OPA sidecar + Rego)

Each handler asks OPA for a yes/no decision before touching the kube API. OPA runs as a sidecar in the Porthole pod; the policy is mounted from a ConfigMap.

| Env var | Example |
|---|---|
| `OPA_URL` | `http://localhost:8181/v1/data/porthole/authz/decision` |

When `OPA_URL` is unset Porthole allows everything (logged in the startup banner). That makes it safe to omit OPA for local dev — just don't forget to set it in production.

### Input shape

```json
{
  "input": {
    "user":             { "sub": "...", "email": "...", "groups": ["..."] },
    "request":          { "action": "inject_ec", "namespace": "team-a-prod", "pod": "target" },
    "now":              "2026-06-07T14:00:00Z",
    "namespace_labels": { "team": "a", "tier": "production" }
  }
}
```

The action vocabulary is fixed (defined in `pkg/auth/opa.go` and the Rego both):

- `list_namespaces`, `list_pods`, `list_ec`
- `inject_ec`, `attach_ec`

### Policy shape

`policy/data.json` defines two tables: **roles** (action bundles) and **bindings** (group → role → namespace scope).

```json
{
  "policy": {
    "roles": {
      "viewer":   ["list_namespaces", "list_pods", "list_ec"],
      "debugger": ["list_namespaces", "list_pods", "list_ec", "inject_ec", "attach_ec"],
      "admin":    ["list_namespaces", "list_pods", "list_ec", "inject_ec", "attach_ec"]
    },
    "bindings": [
      { "group": "porthole-admins", "role": "admin",    "namespace_glob": "*" },
      { "group": "team-a",          "role": "debugger", "namespace_glob": "team-a-*" },

      { "group": "secops",          "role": "debugger",
        "namespace_labels": { "tier": "production" } },

      { "group": "tenant-acme",     "role": "debugger",
        "namespace_glob":   "tenant-*",
        "namespace_labels": { "tenant": "acme" } },

      { "group": "junior-devs",     "role": "debugger",
        "namespace_glob":   "dev-*",
        "business_hours":   true }
    ]
  }
}
```

A binding may scope by **glob**, by **labels**, or by **both** (logical AND). Cluster-wide actions (`list_namespaces`) need an unconditional `namespace_glob: "*"` — a label-scoped binding cannot grant them, by design.

`business_hours: true` restricts the binding to Mon-Fri 09:00–17:00 UTC.

### Namespace labels

When a request targets a namespace, Porthole looks up that namespace's labels (`pkg/authdata`, 60s TTL cache, fail-open) and includes them in the OPA input. That lets policies route by `tier=production`, `team=a`, or any other label your platform already stamps onto namespaces.

### Editing the policy

```sh
$EDITOR policy/porthole.rego policy/data.json
make opa-eval         # 15-case local sanity check, no cluster required
make opa-configmap    # regenerate deploy/opa/policy-configmap.yaml from policy/
make opa-apply        # kubectl apply the ConfigMap; OPA hot-reloads
```

## Audit log

One structured `slog` JSON line per security-relevant decision, written to stdout. The schema lines up with the action constants — a SIEM rule keying off `action` catches every inject and every attach deny.

```json
{
  "time":             "2026-06-07T10:53:45Z",
  "level":            "INFO|WARN",
  "msg":              "inject|attach",
  "action":           "inject|attach_ec",
  "user":             "<sub claim>",
  "source_ip":        "10.0.1.5",
  "namespace":        "demo",
  "pod":              "target",
  "image":            "busybox:1.36",
  "duration_ms":      53,
  "outcome":          "success|error|denied",
  "debug_container":  "porthole-a2045e61",
  "reason":           "default deny",
  "error":            "denied: default deny"
}
```

Successful attaches are intentionally not audited per-byte; the *start* of an attach session shows in gin's access log, and an authZ-deny on attach lands here as `outcome:"denied"` with the OPA reason.

## Configuration reference

| Env var          | Default                  | What it does |
|------------------|--------------------------|---|
| `PORT`           | `8081`                   | Single HTTP port — SPA, REST, WS, all here. |
| `AUTH_DISABLED`  | _(unset)_                | `true` → skip JWT validation, stamp a `local-dev` principal. |
| `JWKS_URL`       | _(required)_             | Keycloak JWKS endpoint, used to validate inbound JWTs. |
| `OIDC_ISSUER`    | _(optional)_             | Expected `iss` claim. Empty disables the check. |
| `OIDC_AUDIENCE`  | _(optional)_             | Expected `aud` claim. Empty disables the check. |
| `OPA_URL`        | _(unset → OPA disabled)_ | OPA decision endpoint, e.g. `http://localhost:8181/v1/data/porthole/authz/decision`. |

## Development

```sh
make build                # go build ./...
make run-local            # go run . (uses your current kubectl context)
make opa-eval             # 15-case Rego sanity check
make envoy-smoke          # ROPC against Keycloak → curl Porthole through the gateway
```

The SPA lives at `pkg/web/dist/` and is embedded into the binary via `pkg/web/embed.go`. Edit and re-`go build`; no separate frontend build step.

## Roadmap

- `forwardIDToken` instead of `forwardAccessToken` once Envoy Gateway implements it (`pkg/auth/jwt.go` already accepts `X-ID-Token`).
- OPA bundle server instead of in-pod ConfigMap (signed bundles, central policy ops).
- Resize-aware pod-side terminal (we already plumb client size; ANSI bracketed-paste / focus events next).
- Optional non-TTY `/debug/exec` for one-shot programmatic use.

## Resources

- [How `kubectl exec` works](https://erkanerol.github.io/post/how-kubectl-exec-works/)
- [Ephemeral containers with client-go](https://github.com/iximiuz/client-go-examples/blob/main/patch-add-ephemeral-container/main.go)
- [Envoy Gateway SecurityPolicy / OIDC](https://gateway.envoyproxy.io/docs/tasks/security/oidc/)
- [OPA Rego language](https://www.openpolicyagent.org/docs/latest/policy-language/)
