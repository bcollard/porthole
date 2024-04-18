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
curl -s $IP:${PORT}/debug/inject -X POST -d '{"namespace":"default","pod":"'${HTTPBIN_POD}'", "command":"echo foo"}' | jq
curl -s $IP:${PORT}/debug/inject -X POST -d '{"namespace":"default","pod":"'${HTTPBIN_POD}'"}' | jq


# THE IDEA
porthole run ns/pod --image busybox -- curl service-foo:8081
# returns the logs
porthole term ns/pod --image busybox
# opens a terminal (WS connection)



curl $IP:${PORT}/debug/inject -X POST \
  -d '{"namespace":"default", "pod":"'${HTTPBIN_POD}'"}' | jq
  -d '{"namespace":"default", "pod":"'${HTTPBIN_POD}'", "image": "busybox"}' | jq

#curl $IP:${PORT}/debug/run -X POST \
#  -d '{"namespace":"default", "pod":"'${HTTPBIN_POD}'", "image": "busybox", "command":"echo foo"}' | jq
## blocking, wait for the response, then return the output

websocat $IP:${PORT}/debug/term -X POST \
  -d '{"namespace":"default", "pod":"'${HTTPBIN_POD}'"}' | jq
# open bidirectional connection

