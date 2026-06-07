# Envoy Gateway example deployment

End-to-end on a fresh `kind` cluster:

1. Install Envoy Gateway and wait for the controller.
2. Install Porthole (this chart) with `gateway.enabled=true` and `gateway.oidc.enabled=true`.
3. Bootstrap a Keycloak realm/client/user (or point at any other OIDC IdP).
4. Open the gateway IP + your `/etc/hosts` mapping in a browser.

## Prerequisites

- A running cluster (e.g. `kind create cluster --name porthole-demo`)
- `helm`, `kubectl`, `curl` on your PATH
- An OIDC IdP — anything Envoy Gateway's `SecurityPolicy.oidc` accepts
  (Keycloak, Auth0, Dex, …). The sibling example
  [`../porthole/`](../porthole/) shows how to bootstrap a Keycloak realm
  with `scripts/keycloak-bootstrap.sh`.

## 1. Install Envoy Gateway

```sh
helm upgrade --install eg \
  oci://docker.io/envoyproxy/gateway-helm \
  --version v1.6.0 \
  -n envoy-gateway-system --create-namespace
kubectl -n envoy-gateway-system rollout status deploy/envoy-gateway --timeout=120s
```

This brings up the Envoy Gateway controller and the default `GatewayClass`
named `eg`. The Porthole chart's `Gateway` references that class by
default (`gateway.className=eg`).

## 2. Install Porthole with OIDC

```sh
# Replace the OIDC values to match your IdP. The clientSecret is created
# as a kube Secret in the same namespace, referenced by the SecurityPolicy.
helm install porthole ../../helm-chart/porthole \
  --namespace porthole --create-namespace \
  --values values.yaml \
  --set gateway.oidc.issuer="https://keycloak.example.com/realms/porthole" \
  --set gateway.oidc.clientID="porthole" \
  --set gateway.oidc.clientSecret="<paste-from-keycloak-bootstrap>" \
  --set auth.jwksURL="https://keycloak.example.com/realms/porthole/protocol/openid-connect/certs" \
  --set auth.issuer="https://keycloak.example.com/realms/porthole"
```

See [`values.yaml`](./values.yaml) for the curated set of overrides this
example uses — it enables the Gateway, the OIDC `SecurityPolicy`, the OPA
sidecar, and the 30-minute EC sweeper.

## 3. Wire up DNS (kind only)

`kind` has no LoadBalancer. The easiest workaround on a laptop is the
Envoy Gateway docs' `MetalLB` add-on, or a simple `kubectl port-forward`
into the gateway's NodePort + a `/etc/hosts` entry pointing the gateway
hostname at `127.0.0.1`.

```sh
# Port-forward the gateway listener (port-name "http" or "https"
# depending on whether you set gateway.tlsSecretName).
GW_NS=envoy-gateway-system
GW_POD=$(kubectl -n "$GW_NS" get pods -l gateway.envoyproxy.io/owning-gateway-name=porthole -o name | head -1)
kubectl -n "$GW_NS" port-forward "$GW_POD" 8080:10080
```

Then add to `/etc/hosts`:

```
127.0.0.1  porthole.example.com
```

and open <http://porthole.example.com:8080/ui/>. The OIDC redirect dance
will route you through Keycloak; after login the SPA loads.

## 4. Try it out

- Pick a namespace, pick a pod, click **+ Debugger**.
- Once the EC is running you'll be attached automatically.
- Click **Clean up all** to terminate every porthole-injected EC in that pod.
- Or just walk away — the 30-minute `ecSweepTTL` in the example values
  will reap forgotten ECs on its own.

## Teardown

```sh
helm uninstall porthole -n porthole
helm uninstall eg -n envoy-gateway-system
kubectl delete ns porthole envoy-gateway-system
```
