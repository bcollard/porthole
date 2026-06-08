.ONESHELL:
.DEFAULT_GOAL := help

CLUSTER         ?= porthole-demo
ENVOY_VERSION   ?= v1.6.0

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

# ----------------------------------------------------------------------
# Cluster lifecycle
# ----------------------------------------------------------------------

cluster-up: ## Create a kind cluster
	kind create cluster --name $(CLUSTER)
	kubectl config use-context kind-$(CLUSTER)

cluster-down: ## Delete the kind cluster
	kind delete cluster --name $(CLUSTER)

# ----------------------------------------------------------------------
# Local dev
# ----------------------------------------------------------------------

build: ## go build (host binary, sanity)
	go build ./...

run-local: ## Run the binary on the host (uses your current kubectl context)
	go run .

# ----------------------------------------------------------------------
# Envoy Gateway
# ----------------------------------------------------------------------

envoy-install: ## Install Envoy Gateway via Helm into envoy-gateway-system
	helm upgrade --install eg \
	  oci://docker.io/envoyproxy/gateway-helm \
	  --version $(ENVOY_VERSION) \
	  -n envoy-gateway-system --create-namespace
	kubectl -n envoy-gateway-system rollout status deploy/envoy-gateway --timeout=120s

envoy-uninstall: ## Helm uninstall Envoy Gateway
	helm uninstall eg -n envoy-gateway-system || true

# ----------------------------------------------------------------------
# Keycloak
# ----------------------------------------------------------------------

keycloak-bootstrap: ## Create realm + client + user in Keycloak (curl + Admin REST API). KEYCLOAK_URL env required.
	./scripts/keycloak-bootstrap.sh

# ----------------------------------------------------------------------
# OPA authZ
# ----------------------------------------------------------------------

opa-eval: ## Run the policy locally against representative inputs
	./scripts/opa-eval.sh

# ----------------------------------------------------------------------
# Smoke tests
# ----------------------------------------------------------------------

envoy-smoke: ## Use ROPC against Keycloak to fetch a token, then call Porthole via the gateway
	@./scripts/envoy-smoke.sh
