# fetch the IP of the load balancer and curl the porthole service
IP=$(kubectl get svc -n default porthole -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "IP: $IP"

# test the /explore entrypoint
curl -s $IP:${PORT}/explore | jq
curl -s $IP:${PORT}/explore/default | jq

# get pod name for label app=httpbin
HTTPBIN_POD=$(kubectl get pods -n default -l app=httpbin -o jsonpath='{.items[0].metadata.name}')
echo "HTTPBIN_POD: $HTTPBIN_POD"

# list ephemeral containers for a given pod
curl -s $IP:${PORT}/debug/list -X GET -d '{"namespace":"default","pod":"'${HTTPBIN_POD}'"}' | jq

# inject a container into the pod with command
curl -s $IP:${PORT}/debug/inject -X POST -d '{"namespace":"default","pod":"'${HTTPBIN_POD}'", "command":"exec sh -c echo toto"}' | jq

# clear the injected containers
curl -s $IP:${PORT}/debug/clear -X POST -d '{"namespace":"default","pod":"'${HTTPBIN_POD}'"}' | jq
