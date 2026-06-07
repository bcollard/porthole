#!/usr/bin/env bash
# Evaluate the Porthole authZ policy locally against a few representative
# inputs. Useful before pushing a policy change to the cluster.
#
# Requires: the `opa` CLI on $PATH.

set -euo pipefail

POLICY_DIR="${POLICY_DIR:-policy}"
NOW="${NOW:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

if ! command -v opa >/dev/null 2>&1; then
  echo "missing 'opa' CLI; install via: brew install opa" >&2
  exit 1
fi

run_case () {
  local label="$1"; shift
  local input="$1"; shift
  echo "▸ $label"
  echo "  input: $input"
  local out
  out=$(opa eval --format pretty \
    -d "$POLICY_DIR" \
    --stdin-input \
    "data.porthole.authz.decision" <<<"$input")
  echo "  decision: $out"
  echo
}

# 1. admin can inject anywhere
run_case "admin inject ok" '{
  "user":{"sub":"a","groups":["porthole-admins"]},
  "request":{"action":"inject_ec","namespace":"production","pod":"x"},
  "now":"'"$NOW"'"}'

# 2. viewer cannot inject
run_case "viewer inject denied" '{
  "user":{"sub":"v","groups":["porthole-viewers"]},
  "request":{"action":"inject_ec","namespace":"production","pod":"x"},
  "now":"'"$NOW"'"}'

# 3. team-a debugger ok in their ns
run_case "team-a in team-a-prod ok" '{
  "user":{"sub":"d","groups":["team-a"]},
  "request":{"action":"inject_ec","namespace":"team-a-prod","pod":"x"},
  "now":"'"$NOW"'"}'

# 4. team-a debugger denied in team-b ns
run_case "team-a in team-b-prod denied" '{
  "user":{"sub":"d","groups":["team-a"]},
  "request":{"action":"inject_ec","namespace":"team-b-prod","pod":"x"},
  "now":"'"$NOW"'"}'

# 5. junior-devs allowed during business hours
run_case "junior-devs Monday 10:00 UTC ok" '{
  "user":{"sub":"j","groups":["junior-devs"]},
  "request":{"action":"inject_ec","namespace":"dev-1","pod":"x"},
  "now":"2026-06-08T10:00:00Z"}'

# 6. junior-devs denied at night
run_case "junior-devs Monday 22:00 UTC denied" '{
  "user":{"sub":"j","groups":["junior-devs"]},
  "request":{"action":"inject_ec","namespace":"dev-1","pod":"x"},
  "now":"2026-06-08T22:00:00Z"}'

# 7. junior-devs denied on weekend
run_case "junior-devs Saturday 10:00 UTC denied" '{
  "user":{"sub":"j","groups":["junior-devs"]},
  "request":{"action":"inject_ec","namespace":"dev-1","pod":"x"},
  "now":"2026-06-06T10:00:00Z"}'

# 8. list_namespaces cluster-wide ok for any binding with namespace_glob "*"
run_case "viewer list_namespaces ok" '{
  "user":{"sub":"v","groups":["porthole-viewers"]},
  "request":{"action":"list_namespaces","namespace":""},
  "now":"'"$NOW"'"}'

# 9. anonymous (no groups) → default deny
run_case "anonymous → deny" '{
  "user":{"sub":"?","groups":[]},
  "request":{"action":"list_namespaces","namespace":""},
  "now":"'"$NOW"'"}'

# ---- namespace label-based bindings ----

# 10. secops can attach in a ns labelled tier=production
run_case "secops in prod-labelled ns ok" '{
  "user":{"sub":"s","groups":["secops"]},
  "request":{"action":"attach_ec","namespace":"payments","pod":"p"},
  "namespace_labels":{"tier":"production"},
  "now":"'"$NOW"'"}'

# 11. secops denied in non-production ns (label mismatch)
run_case "secops in staging-labelled ns denied" '{
  "user":{"sub":"s","groups":["secops"]},
  "request":{"action":"attach_ec","namespace":"payments","pod":"p"},
  "namespace_labels":{"tier":"staging"},
  "now":"'"$NOW"'"}'

# 12. secops denied on cluster-wide action (label binding can't grant it)
run_case "secops list_namespaces denied (label binding only)" '{
  "user":{"sub":"s","groups":["secops"]},
  "request":{"action":"list_namespaces","namespace":""},
  "namespace_labels":{},
  "now":"'"$NOW"'"}'

# 13. tenant-acme needs BOTH glob and labels — both match → ok
run_case "tenant-acme matching glob+labels ok" '{
  "user":{"sub":"a","groups":["tenant-acme"]},
  "request":{"action":"inject_ec","namespace":"tenant-prod","pod":"p"},
  "namespace_labels":{"tenant":"acme"},
  "now":"'"$NOW"'"}'

# 14. tenant-acme right glob but wrong labels → deny (AND not OR)
run_case "tenant-acme glob ok, label mismatch denied" '{
  "user":{"sub":"a","groups":["tenant-acme"]},
  "request":{"action":"inject_ec","namespace":"tenant-prod","pod":"p"},
  "namespace_labels":{"tenant":"globex"},
  "now":"'"$NOW"'"}'

# 15. tenant-acme right labels but wrong glob → deny
run_case "tenant-acme label ok, glob mismatch denied" '{
  "user":{"sub":"a","groups":["tenant-acme"]},
  "request":{"action":"inject_ec","namespace":"infra-1","pod":"p"},
  "namespace_labels":{"tenant":"acme"},
  "now":"'"$NOW"'"}'
