#!/usr/bin/env bash
# Bootstraps a realm/client/user in the managed Keycloak at
# https://keycloak.kong.runlocal.dev for the Porthole demo.
#
# Requires: kc CLI authenticated (`kc login`).
# Idempotent: re-running on existing entities is a no-op.

set -euo pipefail

REALM="${REALM:-porthole}"
CLIENT_ID="${CLIENT_ID:-porthole}"
CLIENT_SECRET="${CLIENT_SECRET:-$(openssl rand -hex 16)}"
REDIRECT_URI="${REDIRECT_URI:-http://porthole.bco.runlocal.dev/oauth2/callback}"
USERNAME="${USERNAME:-demo}"
PASSWORD="${PASSWORD:-demo}"
EMAIL="${EMAIL:-demo@example.com}"

echo ">> ensure realm: $REALM"
kc realm create --name "$REALM" || true

echo ">> ensure client: $CLIENT_ID"
kc client create -r "$REALM" \
  --client-id "$CLIENT_ID" \
  --secret "$CLIENT_SECRET" \
  --standard-flow \
  --redirect-uri "$REDIRECT_URI" || true

echo ">> ensure user: $USERNAME"
kc user create -r "$REALM" --username "$USERNAME" || true
kc user set-password -r "$REALM" --username "$USERNAME" --password "$PASSWORD"

cat <<EOF

=== Porthole OIDC bootstrap ===
issuer:        https://keycloak.kong.runlocal.dev/realms/$REALM
client_id:     $CLIENT_ID
client_secret: $CLIENT_SECRET
redirect_uri:  $REDIRECT_URI
test user:     $USERNAME / $PASSWORD ($EMAIL)

Next steps:
  cp deploy/envoy-gateway/secret.example.yaml deploy/envoy-gateway/secret.yaml
  # replace REPLACE_ME with: $CLIENT_SECRET
  kubectl apply -f deploy/envoy-gateway/secret.yaml
  make gateway-apply
EOF
