# oauth2-proxy + ingress-nginx example

OIDC in front of porthole using the most common OSS pattern:
[**oauth2-proxy**](https://oauth2-proxy.github.io/oauth2-proxy/) as the
auth layer, [**ingress-nginx**](https://kubernetes.github.io/ingress-nginx/)
as the data-plane. oauth2-proxy is invoked via the nginx `auth_request`
sub-handler — when the session cookie is missing, nginx redirects the
browser to `/oauth2/start`; on the upstream request it forwards an
`Authorization: Bearer <id_token>` header that the porthole JWT
middleware validates.

This is the second OIDC-gateway recipe (the first lives in
[`../envoy-gateway/`](../envoy-gateway/)).

## Moving parts

1. **ingress-nginx** — the data-plane.
2. **oauth2-proxy** — exposes `/oauth2/*` (sign-in, auth, callback) and
   sets `Authorization: Bearer <id_token>` upstream.
3. **porthole chart** with `ingress.enabled=true` and the right
   nginx annotations to invoke oauth2-proxy via `auth_request`.

## TL;DR

```sh
# 1. ingress-nginx
helm upgrade --install ingress-nginx \
  oci://ghcr.io/nginxinc/charts/ingress-nginx \
  -n ingress-nginx --create-namespace

# 2. OIDC bootstrap (Keycloak, but any OIDC IdP works).
KEYCLOAK_URL=http://localhost:8080 ../../../scripts/keycloak-bootstrap.sh
# ^ prints client_id and client_secret — feed both to oauth2-proxy below.

# 3. oauth2-proxy
helm upgrade --install oauth2-proxy \
  oauth2-proxy/oauth2-proxy \
  -n oauth2-proxy --create-namespace \
  -f oauth2-proxy-values.yaml \
  --set config.clientID=porthole \
  --set config.clientSecret=<paste-from-step-2> \
  --set config.cookieSecret=$(openssl rand -base64 32 | tr -- '+/' '-_' | tr -d '=')

# 4. Porthole. The values.yaml below wires the ingress-nginx
# auth_request annotations at the porthole Ingress.
helm install porthole ../../../helm-chart/porthole \
  -n porthole --create-namespace \
  -f values.yaml \
  --set auth.jwksURL=http://localhost:8080/realms/porthole/protocol/openid-connect/certs \
  --set auth.issuer=http://localhost:8080/realms/porthole
```

## How the bearer token reaches porthole

oauth2-proxy is configured (in `oauth2-proxy-values.yaml`) with
`set_authorization_header=true` and `pass_authorization_header=true`.
The nginx Ingress runs `auth_request` against oauth2-proxy's `/oauth2/auth`
endpoint on every porthole request; the response includes the
`Authorization: Bearer <id_token>` header, which nginx then forwards
upstream via `auth-response-headers: Authorization`.

Porthole's JWT middleware sees that header, validates the token
against the Keycloak JWKS, and stamps the principal.

## Caveat — WebSockets

ingress-nginx's `auth_request` does **not** run on the WebSocket
upgrade, so the `/term/*` route would skip auth unless the browser
already has a valid session cookie. Keep `wsAllowedOrigins` set to
your public hostname (already configured in `values.yaml`) so a
cross-origin WS upgrade is rejected even if the cookie is valid —
that's the CSWSH defence the porthole binary itself enforces.

## Files

- [`values.yaml`](./values.yaml) — porthole chart values: `ingress.enabled`,
  the nginx `auth-url` / `auth-signin` / `auth-response-headers`
  annotations.
- [`oauth2-proxy-values.yaml`](./oauth2-proxy-values.yaml) —
  oauth2-proxy chart values: OIDC provider, header passing.
