# porthole — Helm chart

Web-based debug terminal for Kubernetes. Pick a pod, inject an ephemeral
container with any debug image (`netshoot`, `busybox`, `psql`, …), attach
from the browser. JWT-based authN (the chart accepts any IdP whose JWKS
you point it at) plus an OPA sidecar for authZ.

The chart is **gateway-implementation neutral** — it does *not* render
Envoy Gateway / Istio / Pomerium / oauth2-proxy CRDs. Pair it with the
OIDC layer of your choice; see the recipes under `docs/examples/`.

## TL;DR — port-forward, no auth

```sh
helm install porthole ./helm-chart/porthole \
  --namespace porthole --create-namespace \
  --set auth.disabled=true
kubectl -n porthole port-forward svc/porthole 8081:8081
open http://localhost:8081/ui/
```

With `auth.disabled=true`, every request is stamped as the `local-dev`
principal. For anything beyond a kubectl-port-forward demo, point an
OIDC-aware front at it (`docs/examples/`).

## What the chart deploys

| Resource | Purpose |
|---|---|
| `Deployment` | porthole container + optional OPA sidecar |
| `Service` (default ClusterIP, can be LoadBalancer) | port 8081 |
| `ServiceAccount` | identity for the kube API |
| `ClusterRole` + `ClusterRoleBinding` | get/list/watch pods + namespaces, exec/attach/patch ephemeralcontainers cluster-wide |
| `ConfigMap` | inline OPA policy + data (when `opa.enabled=true`) |
| `Ingress` | when `ingress.enabled=true` |
| `HTTPRoute` (+ optional `Gateway`) | when `gatewayAPI.enabled=true` |

What the chart **does not** deploy: any vendor-specific CRD (Envoy
Gateway `SecurityPolicy`, Pomerium routes, Authorino `AuthConfig`,
oauth2-proxy `Deployment`, …). Those belong with the OIDC layer the
operator owns. See [`../../docs/examples/`](../../docs/examples/).

## Exposing the service

Pick **one** (or none, and use `kubectl port-forward`):

- `service.type: LoadBalancer` — simplest, no extra resources.
- `ingress.enabled: true` — standard k8s `Ingress` with your existing
  controller (ingress-nginx, Traefik, …).
- `gatewayAPI.enabled: true` — Gateway API `HTTPRoute` attached to a
  Gateway you operate (Envoy Gateway, NGINX Gateway, Istio, Cilium,
  …). Optionally also render a `Gateway` (`gatewayAPI.gateway.create`).

## Key values

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `ghcr.io/bcollard/porthole` | |
| `image.tag` | `Chart.AppVersion` | override per release |
| `auth.disabled` | `false` | `true` skips JWT, stamps a `local-dev` principal |
| `auth.jwksURL` | _(empty)_ | required when `auth.disabled=false` |
| `auth.issuer` / `auth.audience` | _(empty)_ | optional `iss` / `aud` checks |
| `wsAllowedOrigins` | _(empty)_ | comma-separated allowlist of `Origin` headers for `/term` WS upgrades. CSWSH defence — set this whenever you front porthole with anything cookie-authenticated. |
| `opa.enabled` | `true` | runs an OPA sidecar; policy comes from inline values |
| `opa.policy` / `opa.data` | _(default policy)_ | replace to bind your own groups/roles/namespaces |
| `ecSweepTTL` | _(empty)_ | e.g. `30m` — auto-terminate porthole-injected ECs older than this |
| `service.type` | `ClusterIP` | switch to `LoadBalancer` to surface directly |
| `ingress.enabled` | `false` | render an `Ingress`; pair with annotations that wire OIDC (oauth2-proxy / Authelia / …) |
| `gatewayAPI.enabled` | `false` | render an `HTTPRoute`; optionally a `Gateway` too |

See [`values.yaml`](./values.yaml) for the full schema.

## Upgrading the OPA policy

The policy + data sit in the chart values. Edit and `helm upgrade`:

```sh
helm upgrade porthole ./helm-chart/porthole \
  --reuse-values \
  --set-file opa.policy=./policy/porthole.rego \
  --set-file opa.data=./policy/data.json
```

OPA hot-reloads the mounted ConfigMap; no pod restart required.

## Uninstall

```sh
helm uninstall porthole -n porthole
```
