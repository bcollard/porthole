.ONESHELL:
.DEFAULT_GOAL := help

CLUSTER         ?= porthole-e2e
NAMESPACE       ?= default
PORT            ?= 8081
HOST            ?= porthole.bco.runlocal.dev
ENVOY_VERSION   ?= v1.6.0

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

# ----------------------------------------------------------------------
# Cluster lifecycle
# ----------------------------------------------------------------------

cluster-up: ## klimax: create the kind cluster
	klimax cluster create $(CLUSTER)
	kubectl config use-context $(CLUSTER)

cluster-down: ## klimax: delete the kind cluster
	klimax cluster delete $(CLUSTER)

# ----------------------------------------------------------------------
# Subdomain + wildcard cert (one-time per laptop / GCP project)
# ----------------------------------------------------------------------

runlocal-bootstrap: ## Create bco.runlocal.dev DNS zone + wildcard cert + install secret
	./scripts/runlocal-subdomain-bootstrap.sh

# ----------------------------------------------------------------------
# Porthole image + deploy (ko)
# ----------------------------------------------------------------------

build: ## go build (host binary, sanity)
	go build ./...

run-local: ## Run the binary directly on the host (kubeconfig from current context)
	go run .

deploy: ## Build the container image with ko and apply RBAC + Deployment + Service
	ko apply -B -f deploy/rbac.yaml
	ko apply -B -f deploy/deployment-ko.yaml
	kubectl apply -f deploy/envoy-gateway/20-service.yaml

undeploy: ## Remove Porthole resources
	kubectl delete -f deploy/envoy-gateway/20-service.yaml --ignore-not-found
	kubectl delete -f deploy/deployment-ko.yaml --ignore-not-found
	kubectl delete -f deploy/rbac.yaml --ignore-not-found

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

gateway-apply: ## Apply Gateway, HTTPRoute, SecurityPolicy
	@if ! kubectl get secret wildcard-bco-runlocal-dev >/dev/null 2>&1; then \
	  echo "!! Missing TLS secret 'wildcard-bco-runlocal-dev'. Run 'make runlocal-bootstrap' first."; \
	  exit 1; \
	fi
	kubectl apply -f deploy/envoy-gateway/10-gateway.yaml
	kubectl apply -f deploy/envoy-gateway/30-httproute.yaml
	@if ! kubectl get secret porthole-oidc-secret >/dev/null 2>&1; then \
	  echo "!! Missing secret 'porthole-oidc-secret'. Run 'make keycloak-bootstrap' then apply your secret.yaml."; \
	  exit 1; \
	fi
	kubectl apply -f deploy/envoy-gateway/40-securitypolicy.yaml

gateway-delete: ## Remove Gateway + Route + SecurityPolicy
	kubectl delete -f deploy/envoy-gateway/40-securitypolicy.yaml --ignore-not-found
	kubectl delete -f deploy/envoy-gateway/30-httproute.yaml --ignore-not-found
	kubectl delete -f deploy/envoy-gateway/10-gateway.yaml --ignore-not-found

gateway-ip: ## Print the LoadBalancer IP assigned to the Envoy Gateway
	@kubectl get gateway porthole-gateway -o jsonpath='{.status.addresses[0].value}{"\n"}'

# ----------------------------------------------------------------------
# Keycloak (managed instance)
# ----------------------------------------------------------------------

keycloak-bootstrap: ## Create realm + client + demo user in the managed Keycloak
	./scripts/keycloak-bootstrap.sh

# ----------------------------------------------------------------------
# OPA authZ
# ----------------------------------------------------------------------

opa-eval: ## Run the policy locally against representative inputs
	./scripts/opa-eval.sh

opa-apply: ## Apply the OPA policy ConfigMap to the cluster
	kubectl apply -f deploy/opa/policy-configmap.yaml
	@echo "Rollout porthole if it's already running:"
	@echo "  kubectl rollout restart deploy/porthole"

opa-configmap: ## Regenerate deploy/opa/policy-configmap.yaml from policy/*
	kubectl create configmap porthole-opa-policy \
	  --from-file=policy/porthole.rego \
	  --from-file=policy/data.json \
	  --dry-run=client -o yaml > deploy/opa/policy-configmap.yaml
	@echo "wrote deploy/opa/policy-configmap.yaml"

# ----------------------------------------------------------------------
# End-to-end
# ----------------------------------------------------------------------

e2e: cluster-up runlocal-bootstrap envoy-install deploy keycloak-bootstrap ## Full setup (then apply secret.yaml + 'make gateway-apply')
	@echo "Now: cp deploy/envoy-gateway/secret.example.yaml deploy/envoy-gateway/secret.yaml"
	@echo "      (paste client_secret from keycloak-bootstrap into secret.yaml)"
	@echo "      kubectl apply -f deploy/envoy-gateway/secret.yaml"
	@echo "      make gateway-apply"
	@echo "      open https://$(HOST)/ui/"

# ----------------------------------------------------------------------
# Smoke tests
# ----------------------------------------------------------------------

envoy-smoke: ## Use ROPC against Keycloak to fetch a token, then call Porthole via the gateway
	@./scripts/envoy-smoke.sh

# ----------------------------------------------------------------------
# Legacy
# ----------------------------------------------------------------------

print-ip: ## Print the IP of the porthole Service
	@echo $$(kubectl get svc -n $(NAMESPACE) porthole -o jsonpath='{.status.loadBalancer.ingress[0].ip}'):$(PORT)/

clean: undeploy ## Alias for undeploy
