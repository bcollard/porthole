#!/usr/bin/env bash
# keycloak-bootstrap.sh
#
# Idempotently provisions a Keycloak realm/client/user/group for
# Porthole, using only curl against the Admin REST API. Works with
# any Keycloak ≥ 18 (the protocol hasn't changed in years).
#
# Required env vars:
#   KEYCLOAK_URL    Base URL of the Keycloak instance (e.g. http://localhost:8080)
#   ADMIN_USER      master-realm admin username (default: admin)
#   ADMIN_PASSWORD  master-realm admin password (default: admin)
#
# Optional env vars (with sensible defaults):
#   REALM           realm to create / use            (default: porthole)
#   CLIENT_ID       OIDC client id                   (default: porthole)
#   REDIRECT_URI    OIDC redirect URI                (default: http://porthole.example.com/oauth2/callback)
#   USERNAME        test user name                   (default: demo)
#   PASSWORD        test user password               (default: demo)
#   EMAIL           test user email                  (default: demo@example.com)
#   GROUP           group the user joins             (default: porthole-admins)
#
# On success the script prints the client_secret you'll need to wire
# into the Envoy Gateway SecurityPolicy.

set -euo pipefail

KEYCLOAK_URL="${KEYCLOAK_URL:?set KEYCLOAK_URL (e.g. http://localhost:8080)}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin}"

REALM="${REALM:-porthole}"
CLIENT_ID="${CLIENT_ID:-porthole}"
REDIRECT_URI="${REDIRECT_URI:-http://porthole.example.com/oauth2/callback}"
USERNAME="${USERNAME:-demo}"
PASSWORD="${PASSWORD:-demo}"
EMAIL="${EMAIL:-demo@example.com}"
GROUP="${GROUP:-porthole-admins}"

# ---------- helpers ----------

need() { command -v "$1" >/dev/null || { echo "missing required tool: $1" >&2; exit 1; }; }
need curl
need jq

api() { # api METHOD PATH [JSON_BODY]
  local method="$1" path="$2" body="${3-}"
  local args=(-sS -o /tmp/kc-out -w "%{http_code}" -X "$method"
              -H "Authorization: Bearer $TOKEN")
  if [[ -n "$body" ]]; then
    args+=(-H "Content-Type: application/json" --data "$body")
  fi
  local code
  code=$(curl "${args[@]}" "${KEYCLOAK_URL%/}/admin/realms/$path")
  echo "$code"
}

# ---------- 1. admin token ----------

echo ">> obtaining admin token from $KEYCLOAK_URL"
TOKEN=$(curl -sS -X POST \
  -d "client_id=admin-cli" \
  -d "username=$ADMIN_USER" \
  -d "password=$ADMIN_PASSWORD" \
  -d "grant_type=password" \
  "${KEYCLOAK_URL%/}/realms/master/protocol/openid-connect/token" \
  | jq -r .access_token)
if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
  echo "failed to obtain admin token" >&2
  exit 1
fi

# ---------- 2. realm ----------

echo ">> ensure realm: $REALM"
code=$(curl -sS -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  "${KEYCLOAK_URL%/}/admin/realms/$REALM")
if [[ "$code" == "404" ]]; then
  api POST "" "$(jq -n --arg r "$REALM" '{realm:$r, enabled:true}')" >/dev/null
fi

# ---------- 3. client ----------

echo ">> ensure client: $CLIENT_ID"
client_uuid=$(curl -sS -H "Authorization: Bearer $TOKEN" \
  "${KEYCLOAK_URL%/}/admin/realms/$REALM/clients?clientId=$CLIENT_ID" \
  | jq -r '.[0].id // empty')

if [[ -z "$client_uuid" ]]; then
  api POST "$REALM/clients" "$(jq -n \
      --arg cid "$CLIENT_ID" \
      --arg ru "$REDIRECT_URI" \
      '{
        clientId: $cid,
        enabled: true,
        protocol: "openid-connect",
        publicClient: false,
        standardFlowEnabled: true,
        directAccessGrantsEnabled: true,
        redirectUris: [$ru],
        webOrigins: ["+"],
        attributes: { "post.logout.redirect.uris": "+" }
      }')" >/dev/null
  client_uuid=$(curl -sS -H "Authorization: Bearer $TOKEN" \
    "${KEYCLOAK_URL%/}/admin/realms/$REALM/clients?clientId=$CLIENT_ID" \
    | jq -r '.[0].id')
fi

# Read the (auto-generated) client secret.
CLIENT_SECRET=$(curl -sS -H "Authorization: Bearer $TOKEN" \
  "${KEYCLOAK_URL%/}/admin/realms/$REALM/clients/$client_uuid/client-secret" \
  | jq -r .value)

# ---------- 4. add a "groups" mapper so tokens carry the group claim ----------

echo ">> ensure groups protocol-mapper on client"
has_groups_mapper=$(curl -sS -H "Authorization: Bearer $TOKEN" \
  "${KEYCLOAK_URL%/}/admin/realms/$REALM/clients/$client_uuid/protocol-mappers/models" \
  | jq -r '[.[] | select(.name=="groups")] | length')
if [[ "$has_groups_mapper" == "0" ]]; then
  api POST "$REALM/clients/$client_uuid/protocol-mappers/models" '{
    "name": "groups",
    "protocol": "openid-connect",
    "protocolMapper": "oidc-group-membership-mapper",
    "config": {
      "claim.name": "groups",
      "full.path": "false",
      "id.token.claim": "true",
      "access.token.claim": "true",
      "userinfo.token.claim": "true"
    }
  }' >/dev/null
fi

# ---------- 5. group ----------

echo ">> ensure group: $GROUP"
group_id=$(curl -sS -H "Authorization: Bearer $TOKEN" \
  "${KEYCLOAK_URL%/}/admin/realms/$REALM/groups?search=$GROUP" \
  | jq -r --arg g "$GROUP" '[.[] | select(.name==$g)] | .[0].id // empty')
if [[ -z "$group_id" ]]; then
  api POST "$REALM/groups" "$(jq -n --arg n "$GROUP" '{name:$n}')" >/dev/null
  group_id=$(curl -sS -H "Authorization: Bearer $TOKEN" \
    "${KEYCLOAK_URL%/}/admin/realms/$REALM/groups?search=$GROUP" \
    | jq -r --arg g "$GROUP" '[.[] | select(.name==$g)] | .[0].id')
fi

# ---------- 6. user ----------

echo ">> ensure user: $USERNAME"
user_id=$(curl -sS -H "Authorization: Bearer $TOKEN" \
  "${KEYCLOAK_URL%/}/admin/realms/$REALM/users?username=$USERNAME&exact=true" \
  | jq -r '.[0].id // empty')
if [[ -z "$user_id" ]]; then
  api POST "$REALM/users" "$(jq -n \
      --arg u "$USERNAME" --arg e "$EMAIL" \
      '{username:$u, email:$e, enabled:true, emailVerified:true}')" >/dev/null
  user_id=$(curl -sS -H "Authorization: Bearer $TOKEN" \
    "${KEYCLOAK_URL%/}/admin/realms/$REALM/users?username=$USERNAME&exact=true" \
    | jq -r '.[0].id')
fi

# Always (re)set the password — that's idempotent in practice.
api PUT "$REALM/users/$user_id/reset-password" "$(jq -n \
    --arg p "$PASSWORD" \
    '{type:"password", value:$p, temporary:false}')" >/dev/null

# Add the user to the group (idempotent — PUT-ing an existing
# membership is a no-op).
api PUT "$REALM/users/$user_id/groups/$group_id" "" >/dev/null

# ---------- output ----------

cat <<EOF

=== Porthole OIDC bootstrap ===
issuer:        ${KEYCLOAK_URL%/}/realms/$REALM
JWKS:          ${KEYCLOAK_URL%/}/realms/$REALM/protocol/openid-connect/certs
client_id:     $CLIENT_ID
client_secret: $CLIENT_SECRET
redirect_uri:  $REDIRECT_URI
test user:     $USERNAME / $PASSWORD ($EMAIL) in group "$GROUP"

Next: pass these to the helm chart, e.g.

  helm upgrade --install porthole ./helm-chart/porthole \\
    -n porthole --create-namespace \\
    --values docs/examples/envoy-gateway/values.yaml \\
    --set auth.jwksURL=${KEYCLOAK_URL%/}/realms/$REALM/protocol/openid-connect/certs \\
    --set auth.issuer=${KEYCLOAK_URL%/}/realms/$REALM \\
    --set gateway.oidc.issuer=${KEYCLOAK_URL%/}/realms/$REALM \\
    --set gateway.oidc.clientID=$CLIENT_ID \\
    --set gateway.oidc.clientSecret=$CLIENT_SECRET
EOF
