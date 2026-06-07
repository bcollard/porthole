#!/usr/bin/env bash
########################################
# Bootstrap a runlocal.dev sub-zone and a wildcard TLS cert for it.
# scripts/runlocal-subdomain-bootstrap.sh
# ---
# - Creates a GCP-hosted DNS managed zone for bco.runlocal.dev (or
#   $DNS_ZONE_NAME) and wires NS records under the runlocal.dev apex.
# - Issues a wildcard cert (*.bco.runlocal.dev) using lego + Cloud DNS-01.
# - Stores the cert as a kube TLS secret in the current cluster.
#
# Idempotent on re-run: zone create and lego run will no-op when the
# zone / cert are already present.
########################################

set -euo pipefail

# ---- vars (override via env) ----
ACCOUNT_NAME="${ACCOUNT_NAME:-bco}"
DNS_APEX_LITERAL="${DNS_APEX_LITERAL:-runlocal-dev}"
DNS_ZONE_NAME="${DNS_ZONE_NAME:-${ACCOUNT_NAME}.runlocal.dev}"
DNS_ZONE_LITERAL="${DNS_ZONE_LITERAL:-${ACCOUNT_NAME}-runlocal-dev}"

GCP_PROJECT="${GCP_PROJECT:-personal-218506}"
GCP_CREDENTIALS_FILE="${GCP_CREDENTIALS_FILE:-/Users/baptiste.collard@konghq.com/projects/private/kind-on-lima/credentials.json}"
GCP_CONFIGURATION="${GCP_CONFIGURATION:-perso}"
LEGO_EMAIL="${LEGO_EMAIL:-baptiste.collard@gmail.com}"

K8S_NAMESPACE="${K8S_NAMESPACE:-default}"
SECRET_NAME="${SECRET_NAME:-wildcard-${DNS_ZONE_LITERAL}}"

# ---- preflight ----
for cmd in gcloud lego yq kubectl; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required tool: $cmd" >&2
    exit 1
  fi
done

echo ">> using GCP configuration: $GCP_CONFIGURATION"
gcloud config configurations activate "$GCP_CONFIGURATION"

##############
# DNS ZONE
##############
echo ">> ensure managed zone: $DNS_ZONE_LITERAL ($DNS_ZONE_NAME)"
if ! gcloud dns --project="$GCP_PROJECT" managed-zones describe "$DNS_ZONE_LITERAL" >/dev/null 2>&1; then
  gcloud dns --project="$GCP_PROJECT" managed-zones create "$DNS_ZONE_LITERAL" \
    --description="Porthole / Baptiste personal subdomain" \
    --dns-name="${DNS_ZONE_NAME}." \
    --visibility="public" \
    --dnssec-state="off"
else
  echo "   (zone exists, skipping create)"
fi

echo ">> fetch NS records for $DNS_ZONE_NAME"
NS_SERVERS=$(gcloud dns --project="$GCP_PROJECT" record-sets list \
  --zone="$DNS_ZONE_LITERAL" --name="${DNS_ZONE_NAME}." --type="NS" \
  --format yaml | yq '.rrdatas | join(",")')
echo "   NS_SERVERS: $NS_SERVERS"

echo ">> wire NS delegation in apex zone $DNS_APEX_LITERAL"
if gcloud dns --project="$GCP_PROJECT" record-sets describe "${DNS_ZONE_NAME}." \
     --zone="$DNS_APEX_LITERAL" --type="NS" >/dev/null 2>&1; then
  echo "   (delegation already wired, skipping)"
else
  gcloud dns --project="$GCP_PROJECT" record-sets create "${DNS_ZONE_NAME}." \
    --zone="$DNS_APEX_LITERAL" --type="NS" --ttl="3600" --rrdatas="$NS_SERVERS"
fi

##############
# WILDCARD CERT (lego, Cloud DNS-01)
##############
echo ">> issue/renew *.$DNS_ZONE_NAME wildcard cert via lego"
GCE_PROJECT="$GCP_PROJECT" \
GCE_SERVICE_ACCOUNT_FILE="$GCP_CREDENTIALS_FILE" \
lego --email "$LEGO_EMAIL" --dns gcloud -d "*.${DNS_ZONE_NAME}" -a run

CERT_FILE=".lego/certificates/_.${DNS_ZONE_NAME}.crt"
KEY_FILE=".lego/certificates/_.${DNS_ZONE_NAME}.key"

if [[ ! -f "$CERT_FILE" || ! -f "$KEY_FILE" ]]; then
  echo "lego didn't produce $CERT_FILE / $KEY_FILE" >&2
  exit 1
fi

##############
# KUBE SECRET
##############
echo ">> ensure kube TLS secret: $K8S_NAMESPACE/$SECRET_NAME"
kubectl -n "$K8S_NAMESPACE" create secret tls "$SECRET_NAME" \
  --cert="$CERT_FILE" --key="$KEY_FILE" \
  --dry-run=client -o yaml | kubectl apply -f -

cat <<EOF

=== runlocal subdomain bootstrap done ===
zone:        $DNS_ZONE_NAME  (literal: $DNS_ZONE_LITERAL)
delegated:   yes (NS records present under $DNS_APEX_LITERAL)
cert:        $CERT_FILE
key:         $KEY_FILE
kube secret: $K8S_NAMESPACE/$SECRET_NAME

The Envoy Gateway listener references this secret via:
  certificateRefs:
    - kind: Secret
      name: $SECRET_NAME
EOF
