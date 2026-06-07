# Envoy Gateway example

OIDC at the gateway layer via Envoy Gateway's `SecurityPolicy`. Three
moving parts that you wire together yourself: the gateway, the
porthole chart, and the `SecurityPolicy` CR.

## TL;DR

```sh
# 1. Envoy Gateway + a shared Gateway named "eg".
helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.6.0 -n envoy-gateway-system --create-namespace

# 2. OIDC bootstrap (creates realm/client/user, prints client_secret).
KEYCLOAK_URL=http://localhost:8080 ../../../scripts/keycloak-bootstrap.sh

# 3. The OIDC client-secret as a kube Secret.
kubectl create secret generic porthole-oidc \
  --from-literal=client-secret='<paste-from-step-2>'

# 4. Porthole.
helm install porthole ../../../helm-chart/porthole \
  -n porthole --create-namespace \
  -f values.yaml \
  --set auth.jwksURL=http://localhost:8080/realms/porthole/protocol/openid-connect/certs \
  --set auth.issuer=http://localhost:8080/realms/porthole

# 5. The Envoy-Gateway-specific SecurityPolicy CR.
kubectl apply -f envoy-gateway-securitypolicy.yaml
```

`values.yaml` enables the chart's generic `gatewayAPI` rendering (an
`HTTPRoute` targeting the shared `eg` Gateway). The chart deliberately
does not render the `SecurityPolicy` itself — that's
Envoy-Gateway-specific and out of scope for a vendor-neutral chart.
