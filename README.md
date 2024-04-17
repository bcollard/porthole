
## Deployment
```shell
make run
```

## Why
- Developers don't have to proxy to pods in k8s clusters.  
  They'll rather connect to a backend app deployed in the same cluster
  after authenticating through the API Gateway
- To test their own svc, they can attach an ephemeral container to the pod
  and run their tests from there
- To test others' svc (the ones they don't own but are allowed to access), 
  the system relies on mesh policies that are applied in the cluster.
- Cluster might not be configured with the same IdP as the one used by the
  API Gateway. This is a common scenario in large organizations where
  different teams use different IdPs. So the k8s RBAC cannot leverage corporate 
  identities to grant access to namespaces. 
- Abstract away k8s concepts from developers. Otherwise, tooling like Lens
  or k9s would be another possible solution (modulo the OIDC provider issue).

## Authentication
- The API Gateway is the only entry point to the cluster
- It authenticates the user and forwards the request to the backend app
- The backend will fetch dev permissions upon receiving the request,
  using the `id_token` in the request header, and connecting to an
  authorization server that can map user groups to a list of 
  allowed namespaces

## Authorization
TBD - could be based on Ory Oathkeeper, OPA, or a custom solution
