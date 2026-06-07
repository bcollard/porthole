# Porthole — standalone example deployment

Install the chart without a gateway and reach the UI via `kubectl
port-forward`. Smallest possible footprint, no IdP required.

## Install

```sh
kind create cluster --name porthole-demo  # if you don't have one yet
helm install porthole ../../helm-chart/porthole \
  --namespace porthole --create-namespace \
  --values values.yaml
kubectl -n porthole rollout status deploy/porthole --timeout=120s
```

## Open the UI

```sh
kubectl -n porthole port-forward svc/porthole 8081:8081
open http://localhost:8081/ui/
```

With the example `values.yaml`, `auth.disabled=true` is on, so every
request gets stamped as the `local-dev` principal — the default OPA
binding for the `local-dev` group grants full admin. You can drive the
whole UI immediately: list namespaces, inject ECs, attach, clean up.

## What the example values turn on

- `auth.disabled=true` — skip JWT validation; stamp `local-dev` principal.
- `opa.enabled=true` — OPA sidecar with the chart's default policy.
- `ecSweepTTL=15m` — auto-reap forgotten porthole-injected ECs after 15 minutes.

For a real-world deployment with OIDC and a gateway, see
[`../envoy-gateway/`](../envoy-gateway/).

## Upgrade with a custom policy

```sh
helm upgrade porthole ../../helm-chart/porthole \
  --reuse-values \
  --set-file opa.policy=../../policy/porthole.rego \
  --set-file opa.data=../../policy/data.json
```

OPA hot-reloads the mounted ConfigMap — no pod restart needed.

## Teardown

```sh
helm uninstall porthole -n porthole
kind delete cluster --name porthole-demo
```
