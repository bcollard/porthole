# Envoy Gateway + OIDC in front of Porthole

This directory bootstraps an [Envoy Gateway](https://gateway.envoyproxy.io/) sitting in front of Porthole, with an OIDC `SecurityPolicy` that delegates authentication to Keycloak.

Assumptions:

- A kind cluster is up (`make cluster-up`).
- The `bco.runlocal.dev` subdomain + wildcard TLS cert are provisioned (`make runlocal-bootstrap`).
- The managed Keycloak at `https://keycloak.kong.runlocal.dev` is reachable from the cluster.
- DNS for `porthole.bco.runlocal.dev` resolves to the Gateway's LoadBalancer IP — runlocal/MetalLB handles this once the subdomain bootstrap script has run.

## Pieces

| File                         | Purpose |
|------------------------------|---------|
| `10-gateway.yaml`            | Gateway listening on `:443` HTTPS, TLS-terminated with the wildcard cert. |
| `20-service.yaml`            | ClusterIP Service for Porthole (single port 8081 — REST + SPA + WS share an origin). |
| `30-httproute.yaml`          | Routes all of `porthole.bco.runlocal.dev/` → Porthole. Envoy upgrades WS in place. |
| `40-securitypolicy.yaml`     | OIDC SecurityPolicy targeting the HTTPRoute. Pins `forwardAccessToken: true` until EG implements `forwardIDToken`. |
| `secret.example.yaml`        | Template for the Secret carrying the OIDC client secret (do **not** commit a real one). |

## Token propagation

The intent of the design is that Envoy forwards the OIDC **id_token** upstream in a custom header (e.g. `X-ID-Token`), so Porthole can extract identity claims (sub, email, groups) and hand them to an OPA sidecar.

The EG `SecurityPolicy.oidc.forwardIDToken` field exists in the API (see `oidc_types.go`) but is marked `+notImplementedHide` in the v1alpha1 surface as of EG v1.6. That means the controller does not yet wire it through to the Envoy OAuth2 filter.

**Today's fallback:** `forwardAccessToken: true` sends `Authorization: Bearer <access_token>` upstream. Keycloak puts the same identity claims (`groups`, `email`, `preferred_username`) into the access token when the client maps the corresponding scopes, so for our authZ purposes the two tokens are interchangeable at the claim level.

**Migration plan:** when EG ships the implementation, uncomment the `forwardIDToken` block in `40-securitypolicy.yaml`, switch Porthole's middleware to read the `X-ID-Token` header instead of `Authorization`, and drop the `forwardAccessToken` line.

## Bootstrap

```sh
# from repo root
make runlocal-bootstrap   # one-time: DNS zone + wildcard cert in current cluster
make envoy-install        # helm install envoy-gateway into envoy-gateway-system
make keycloak-bootstrap   # uses kc to create realm + client + user. Prints client_secret.
# put the printed secret into a real secret manifest from secret.example.yaml
cp secret.example.yaml secret.yaml   # then edit
kubectl apply -f deploy/envoy-gateway/secret.yaml
make gateway-apply        # apply Gateway, HTTPRoute, SecurityPolicy
open https://porthole.bco.runlocal.dev/ui/
```

## Smoke test (no browser)

`make envoy-smoke` uses curl + the ROPC flow against Keycloak to fetch an access_token, then hits Porthole through the gateway with `Authorization: Bearer …`.
