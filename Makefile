.ONESHELL:
.DEFAULT_GOAL := help

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

run: clean ## Build, deploy and expose the application
	ko apply -B -f deploy
	kubectl expose deployment porthole --type=LoadBalancer --name=porthole --port ${PORT}

print-ip: ## Print the IP address of the exposed service
	@#curl http://$$(kubectl get svc -n default porthole -o jsonpath='{.status.loadBalancer.ingress[0].ip}'):8081/namespaces
	@echo $$(kubectl get svc -n default porthole -o jsonpath='{.status.loadBalancer.ingress[0].ip}'):${PORT}/

clean: ## Delete the k8s resources
	kubectl delete service porthole || true
	kubectl delete -f deploy/ || true
