# Traefik + community OIDC plugin

OIDC in front of porthole using **Traefik OSS** as the data plane and
[`sevensolutions/traefik-oidc-auth`](https://github.com/sevensolutions/traefik-oidc-auth)
as the auth middleware. The plugin is loaded into Traefik via the
built-in [plugin system](https://plugins.traefik.io/) at startup.

> **Caveat — third-party plugin.** Traefik OSS does not ship a native
> OIDC middleware (that lives in Traefik Hub / Traefik Enterprise).
> `traefik-oidc-auth` is a community plugin, freely licensed, but
> independently maintained. Pin a tagged version when you reference it
> from the Traefik static config — and consult the plugin's current
> README for the exact field names; the schema below tracks what's
> stable today and may need light tweaks against newer releases.

## Moving parts

1. **Traefik** with the OIDC plugin declared in its static config.
2. A Traefik **`Middleware`** CR that configures the plugin (issuer,
   client id, client secret, scopes, upstream-header passing).
3. The **porthole chart** rendered with `ingress.enabled=true`, the
   `traefik` ingress class, and the middleware annotation pointing at
   the CR.

## TL;DR

```sh
# 1. Provision an OIDC client (Keycloak shown — any IdP works).
KEYCLOAK_URL=http://localhost:8080 ../../../scripts/keycloak-bootstrap.sh
# ^ prints the client_secret; you'll feed it to the Middleware below.

# 2. Stash the client secret as a kube Secret in porthole's namespace.
kubectl create ns porthole
kubectl -n porthole create secret generic porthole-oidc \
  --from-literal=client-secret='<paste>'

# 3. Install Traefik with the OIDC plugin enabled in static config.
helm upgrade --install traefik traefik/traefik \
  -n traefik --create-namespace \
  -f traefik-values.yaml

# 4. Apply the Middleware CR.
kubectl apply -f oidc-middleware.yaml

# 5. Install porthole; the chart-rendered Ingress carries the
#    middleware annotation that hooks the plugin into the request path.
helm install porthole ../../../helm-chart/porthole \
  -n porthole \
  -f porthole-values.yaml \
  --set auth.jwksURL=http://localhost:8080/realms/porthole/protocol/openid-connect/certs \
  --set auth.issuer=http://localhost:8080/realms/porthole
```

## How the bearer token reaches porthole

`traefik-oidc-auth` terminates the OIDC handshake and stores the
session in a cookie. On every subsequent request it forwards a
configurable `Authorization: Bearer <access_token>` header upstream
(`Headers` block in the Middleware spec). Porthole's JWT middleware
validates that header against the Keycloak JWKS.

The static-config plugin declaration:

```yaml
experimental:
  plugins:
    traefikOidcAuth:
      moduleName: "github.com/sevensolutions/traefik-oidc-auth"
      version: "v0.13.0"   # pin to a tag; check the plugin repo for current
```

is set in `traefik-values.yaml`. The Middleware CR references the
plugin by the same key (`traefikOidcAuth`).

## WebSocket caveat

Traefik runs middleware on the upgrade request, so the OIDC plugin
*does* see the WS handshake. That means the cookie must already exist —
which it will, because the SPA loads `/ui/` over HTTPS first (cookie
gets set), then opens the WebSocket. If you script around the UI
directly you'll need to manage the cookie yourself.

`wsAllowedOrigins` stays set to your public hostname in
`porthole-values.yaml` as a defence-in-depth: if a cookie ever leaks
cross-origin, porthole's built-in `CheckOrigin` still refuses the
upgrade.

## Files

- [`traefik-values.yaml`](./traefik-values.yaml) — Traefik chart values
  with the OIDC plugin declared under `experimental.plugins`.
- [`oidc-middleware.yaml`](./oidc-middleware.yaml) — the `Middleware`
  CR that configures the plugin (issuer, scopes, header forwarding,
  client-secret env ref).
- [`porthole-values.yaml`](./porthole-values.yaml) — porthole chart
  values: `ingress.enabled=true`, `className: traefik`, and the
  `router.middlewares` annotation that attaches the plugin.
