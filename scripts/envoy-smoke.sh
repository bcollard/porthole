#!/usr/bin/env bash
# Smoke test: use the Resource Owner Password Credentials grant to
# get an access token from Keycloak, then call Porthole through the
# Envoy Gateway with that token. Confirms the OIDC plumbing without
# needing a browser.
#
# Prerequisite: the realm/client/user was created via
# scripts/keycloak-bootstrap.sh. ROPC requires the OAuth client to
# have Direct Access Grants enabled — re-toggle that on the client
# in Keycloak if the token request below fails with
# "Direct access grants is not allowed".

set -euo pipefail

ISSUER="${ISSUER:?set ISSUER (e.g. https://keycloak.example.com/realms/porthole)}"
CLIENT_ID="${CLIENT_ID:-porthole}"
CLIENT_SECRET="${CLIENT_SECRET:?set CLIENT_SECRET to the secret printed by keycloak-bootstrap.sh}"
USERNAME="${USERNAME:-demo}"
PASSWORD="${PASSWORD:-demo}"
HOST="${HOST:?set HOST (e.g. porthole.example.com)}"
# Public sub-path porthole is served at (empty = root). Must match
# the chart's gatewayAPI.pathPrefix.
PATH_PREFIX="${PATH_PREFIX:-}"
# Scheme of the gateway listener. The chart's example HTTPS Gateway
# uses 443; set SCHEME=http for a plain Gateway.
SCHEME="${SCHEME:-https}"
# Override only when the cluster has no DNS resolver wired up to the
# Gateway's LoadBalancer (kind without MetalLB, etc.). When set,
# we pin the curl request to that IP via --resolve.
GATEWAY_IP="${GATEWAY_IP:-}"

echo ">> fetching access_token from $ISSUER"
TOKEN_RESP=$(curl -sS -X POST "$ISSUER/protocol/openid-connect/token" \
  -d "grant_type=password" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "username=$USERNAME" \
  -d "password=$PASSWORD" \
  -d "scope=openid profile email")

ACCESS_TOKEN=$(echo "$TOKEN_RESP" | jq -r .access_token)
if [[ -z "$ACCESS_TOKEN" || "$ACCESS_TOKEN" == "null" ]]; then
  echo "failed to obtain access token. Response:"
  echo "$TOKEN_RESP" | jq .
  exit 1
fi

CURL_ARGS=(-sS -H "Authorization: Bearer $ACCESS_TOKEN")
if [[ -n "$GATEWAY_IP" ]]; then
  PORT=443
  [[ "$SCHEME" == "http" ]] && PORT=80
  CURL_ARGS+=(--resolve "$HOST:$PORT:$GATEWAY_IP")
fi

BASE="$SCHEME://$HOST$PATH_PREFIX"
echo ">> calling Porthole at $BASE with bearer token"
curl "${CURL_ARGS[@]}" "$BASE/api/config" | jq .

echo ">> /explore"
curl "${CURL_ARGS[@]}" "$BASE/explore" | jq .
