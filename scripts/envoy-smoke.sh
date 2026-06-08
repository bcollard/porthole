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
GATEWAY_IP="${GATEWAY_IP:-$(kubectl get gateway porthole -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || true)}"

if [[ -z "$GATEWAY_IP" ]]; then
  echo "could not resolve gateway IP — is the Gateway applied?" >&2
  exit 1
fi

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

echo ">> calling Porthole via $HOST ($GATEWAY_IP) with bearer token"
curl -sS -H "Host: $HOST" -H "Authorization: Bearer $ACCESS_TOKEN" \
  "http://$GATEWAY_IP/api/config" | jq .

echo ">> /explore"
curl -sS -H "Host: $HOST" -H "Authorization: Bearer $ACCESS_TOKEN" \
  "http://$GATEWAY_IP/explore" | jq .
