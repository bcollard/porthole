# Envoy Gateway example — shared API hostname at `/porthole`

OIDC at the gateway layer via Envoy Gateway's `SecurityPolicy`. The
example shows the realistic platform-team pattern: porthole shares a
hostname with other backends (`api.example.com`) and lives at
**`/porthole`** rather than getting its own subdomain.

If you'd rather give porthole its own hostname, set
`gatewayAPI.pathPrefix: "/"` and `hostnames: [porthole.example.com]`
in `values.yaml`. Everything else stays the same.

## Moving parts

1. **Envoy Gateway** running, with a `Gateway` named `eg` in the
   default namespace.
2. **`Middleware`-free chart install** — the chart renders an
   `HTTPRoute` that handles the sub-path:
   - 308 redirect `/porthole` → `/porthole/` (so URLs without the
     trailing slash still work)
   - URLRewrite `/porthole/foo` → `/foo` before forwarding
3. **OIDC `SecurityPolicy`** (applied separately) targets that
   HTTPRoute. `redirectURL` and Keycloak's `redirectUris` both include
   the `/porthole` prefix.
4. **No server-side change** in porthole — the SPA infers the public
   prefix from `window.location.pathname` at boot and prepends it to
   every fetch + WebSocket URL.

## TL;DR

```sh
# 1. Envoy Gateway + a shared Gateway named "eg".
helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.6.0 -n envoy-gateway-system --create-namespace

# 2. OIDC bootstrap — note REDIRECT_URI carries the sub-path.
KEYCLOAK_URL=http://localhost:8080 \
  REDIRECT_URI=https://api.example.com/porthole/oauth2/callback \
  ../../../scripts/keycloak-bootstrap.sh
# ^ prints the client_secret you'll use below.

# 3. The OIDC client-secret as a kube Secret.
kubectl create secret generic porthole-oidc \
  --from-literal=client-secret='<paste-from-step-2>'

# 4. Porthole. The chart's HTTPRoute handles the prefix strip.
helm install porthole ../../../helm-chart/porthole \
  -n porthole --create-namespace \
  -f values.yaml \
  --set auth.jwksURL=http://localhost:8080/realms/porthole/protocol/openid-connect/certs \
  --set auth.issuer=http://localhost:8080/realms/porthole

# 5. The Envoy-Gateway-specific SecurityPolicy CR.
kubectl apply -f envoy-gateway-securitypolicy.yaml

# 6. Open it.
open https://api.example.com/porthole/
```

## Why this works without porthole-side changes

The browser navigates to `https://api.example.com/porthole/ui/`. The
SPA's HTML uses **relative paths** for `style.css` and `app.js`, so
the browser asks for them under the same `/porthole/` prefix. The
JavaScript bootstraps a `BASE_PATH` constant from the page URL —
which is `/porthole` here — and prepends it to every `fetch()` and
`new WebSocket()` call.

Server-side, Envoy Gateway has already stripped `/porthole` by the
time the request hits porthole, so the handlers (`/explore`,
`/debug/inject`, `/term/...`) keep mounting at root and never see
the public prefix. One image serves at the root *or* under any
sub-path, no rebuild, no env var.
