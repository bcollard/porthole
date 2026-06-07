# porthole — Helm chart

Web-based debug terminal for Kubernetes. Pick a pod, inject an ephemeral
container with any debug image (`netshoot`, `busybox`, `psql`, …), attach
from the browser. Auth via OIDC at an API gateway, authorization via an
OPA sidecar.

## TL;DR

```sh
helm install porthole ./helm-chart/porthole \
  --namespace porthole --create-namespace \
  --set auth.disabled=true \
  --set opa.enabled=false
kubectl -n porthole port-forward svc/porthole 8081:8081
open http://localhost:8081/ui/
```

That's a no-auth, no-policy install for poking the UI against your current
kube context. For a real deployment, see the examples under
`docs/examples/`:

- [`docs/examples/porthole/`](../../docs/examples/porthole/) — a curated
  `values.yaml` with OIDC, OPA enabled, and the EC sweeper turned on.
- [`docs/examples/envoy-gateway/`](../../docs/examples/envoy-gateway/) —
  install Envoy Gateway alongside porthole and front it with OIDC.

## What the chart deploys

| Resource | Purpose |
|---|---|
| `Deployment` | porthole container + optional OPA sidecar |
| `Service` (ClusterIP) | port 8081 |
| `ServiceAccount` | identity for the kube API |
| `ClusterRole` + `ClusterRoleBinding` | get/list/watch pods + namespaces, exec/attach/patch ephemeralcontainers cluster-wide |
| `ConfigMap` | inline OPA policy + data (when `opa.enabled=true`) |
| `Gateway` + `HTTPRoute` | when `gateway.enabled=true` |
| `SecurityPolicy` + OIDC `Secret` | when `gateway.oidc.enabled=true` (Envoy Gateway CRDs required) |

## Key values

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `ghcr.io/bcollard/porthole` | |
| `image.tag` | `Chart.AppVersion` | override per release |
| `auth.disabled` | `false` | `true` skips JWT, stamps a `local-dev` principal |
| `auth.jwksURL` | _(empty)_ | required when `auth.disabled=false` |
| `auth.issuer` / `auth.audience` | _(empty)_ | optional `iss` / `aud` checks |
| `wsAllowedOrigins` | _(empty)_ | comma-separated allowlist for `/term` WS upgrades; defends against CSWSH. Empty = same-origin only. |
| `opa.enabled` | `true` | runs an OPA sidecar; policy comes from inline values |
| `opa.policy` / `opa.data` | _(default policy)_ | replace to bind your own groups/roles/namespaces |
| `ecSweepTTL` | _(empty)_ | e.g. `30m` — auto-terminate porthole-injected ECs older than this |
| `gateway.enabled` | `false` | render Gateway + HTTPRoute |
| `gateway.oidc.enabled` | `false` | render Envoy Gateway `SecurityPolicy` |

See [`values.yaml`](./values.yaml) for the full schema and all defaults.

## Upgrading the OPA policy

The policy + data sit in the chart's values. Edit them and `helm upgrade`
— the ConfigMap will roll, but OPA hot-reloads files from disk so no pod
restart is required.

```sh
helm upgrade porthole ./helm-chart/porthole \
  --reuse-values \
  --set-file opa.policy=./policy/porthole.rego \
  --set-file opa.data=./policy/data.json
```

## Uninstall

```sh
helm uninstall porthole -n porthole
```
