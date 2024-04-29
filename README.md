## Deployment
```shell
make run
```

## Why
- **SIMPLICITY** - Abstract away Kubernetes concepts from developers. 
  Otherwise, tooling like Lens or k9s would be another possible solution 
  (modulo the OIDC provider issue, see below).  
  Developers don't have to proxy to pods in k8s clusters. They'll rather 
  connect to a backend app (Porthole) deployed in the same cluster (as their 
  app) after authenticating through the API Gateway.
- **FLEXIBILITY** - To test their own service, Developers can attach an 
  ephemeral container to the pod and run their tests from there. It can be any 
  debugging image that they can pull from a registry. So it offers more 
  flexibility if you ever need to use a database client, like `psql` for 
  instance.
- **ZERO-TRUST** - To test others' svc (the ones they don't own but are 
  allowed to access), the system relies on mesh policies that are applied in
  the cluster.
- **CORPORATE** - Cluster might not be configured with the same IdP as the 
  one used by the API Gateway. This is a common scenario in large 
  organizations where different teams use different IdPs. So the k8s RBAC 
  cannot leverage corporate identities to grant access to namespaces. 
  Instead, authentication is delegated to the API Gateway, as explained below.

Other considerations:
- the API GW might not run in the same cluster as the backend app (Porthole)
- there might be no websocket connection possible between the dev laptop and 
  the 
  API-Server of the cluster. But a chain of connections is still possible: `dev 
  laptop/browser -> API GW 
  (Optionally -> Ingress of another cluster) -> backend app (Porthole) -> 
  api-server [-> 
  kubelet 
  -> Container Runtime] -> debug container (part of the pod NS)`  
  Most of these connections will use WebSockets.


## Authentication
- the API Gateway is the only entry point to the cluster
- the API Gateway authenticates the user and forwards the request to the 
  backend app (Porthole)
- the backend app (Porthole) will fetch dev permissions upon receiving the 
  request,
  using the `id_token` in the request header, and connecting to an
  authorization server that can map user roles/groups to a list of 
  allowed namespaces

## Authorization
TBD - could be based on Ory Oathkeeper, OPA, or a custom solution


## Resources

- [api-server to container](https://erkanerol.github.io/post/how-kubectl-exec-works/)
-  