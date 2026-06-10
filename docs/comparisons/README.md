# Porthole vs. similar projects

Porthole is a narrow tool: a browser-based attach-to-pod terminal that
injects ephemeral containers, validates JWTs from an upstream OIDC
gateway, and asks OPA for authorization. This folder compares it to
three projects that overlap in different ways:

- **[Teleport](#teleport)** — same access-control story (corporate
  identity → cluster), much broader scope.
- **[Headlamp](#headlamp)** — same form factor (web UI in the
  cluster), much broader scope.
- **[Lens / OpenLens](#lens--openlens)** — same goal (pick a pod, get
  a shell), different form factor (desktop, kubeconfig-driven).

## Feature matrix

| | **Porthole** | **Teleport** | **Headlamp** | **Lens / OpenLens** |
|---|---|---|---|---|
| Form factor | Web (in-cluster) | Web + CLI (`tsh`) | Web (in-cluster or desktop) | Desktop (Electron) |
| Primary purpose | Attach-to-pod terminal | Access platform (SSH, k8s, DB, RDP, apps) | General-purpose k8s dashboard | General-purpose k8s IDE |
| Exec mechanism | **Ephemeral container** (`debug`-style, any image) | `kubectl exec` into existing container | `kubectl exec` into existing container | `kubectl exec` into existing container |
| Custom debug image | Yes — per-session, any registry | No (uses container's own shell) | No | No |
| Authentication | Validates JWT from upstream OIDC gateway (BYO IdP) | Built-in IdP + SSO connectors (SAML / OIDC / GitHub) | OIDC (built-in) or kubeconfig | Local kubeconfig only |
| Authorization | OPA sidecar (Rego + bindings) | Built-in RBAC (Teleport roles) | Kubernetes RBAC (impersonation) | Kubernetes RBAC (user's kubeconfig) |
| Multi-cluster | No (one cluster per install) | Yes (cluster of clusters) | Yes | Yes |
| Audit log | Structured slog JSON per inject / attach / cleanup | Yes, with **session recording / replay** | No native audit (k8s audit log applies) | No (runs on user's laptop) |
| Footprint | One Deployment + OPA sidecar | Multi-component (auth, proxy, agents, backend store) | One Deployment | Desktop install per user |
| License | Apache-2.0 | Apache-2.0 (Community) + paid Enterprise | Apache-2.0 (CNCF Sandbox) | Lens: closed source under Mirantis EULA. OpenLens: MIT |

## Teleport

[goteleport.com](https://goteleport.com/) — identity-aware access
platform for SSH, Kubernetes, databases, Windows desktops, and
internal apps. Operated by Gravitational/Teleport Inc.

**Where it overlaps.** Teleport's Kubernetes Access mode does the
same job at the policy layer: corporate identity in, ephemeral
short-lived cluster credentials out, every action audited. Like
Porthole it shields the cluster from having to know your users
directly.

**Where it differs.** Teleport is its own identity layer — you wire
SAML/OIDC connectors *into Teleport*, and Teleport mints the access
material. Porthole pushes that responsibility one hop out to whatever
gateway already terminates OIDC (Envoy Gateway, oauth2-proxy,
Pomerium…), reads the resulting JWT, and stops there. Teleport's
authorization is its own RBAC language with its own roles; Porthole
delegates to OPA so you can reuse a policy language you may already
run for other services. Teleport's session is `kubectl exec` into an
existing container; Porthole injects an ephemeral container with the
debug image of your choice, which is what you want when the target
pod's image is a `distroless` or `scratch` with no shell.

Teleport also records sessions and lets you replay them
asciinema-style. Porthole does not.

**Pick Teleport when** you need one tool for SSH + k8s + DBs +
desktops, session replay is a compliance requirement, or you want
the IdP integration owned by the access layer rather than the
gateway. **Pick Porthole when** you already have an OIDC-aware
gateway, you want to debug `scratch`/`distroless` pods, or you don't
want to operate a separate access cluster.

## Headlamp

[headlamp.dev](https://headlamp.dev/) — web-based Kubernetes
dashboard, CNCF Sandbox project, originated at Kinvolk/Microsoft.
Positioned as a modern alternative to the official Kubernetes
Dashboard, with a plugin system.

**Where it overlaps.** Same delivery model: a small in-cluster web
app you point your browser at, with an OIDC option. Headlamp also
has a pod-terminal pane that runs `kubectl exec`-equivalent attach.

**Where it differs.** Headlamp is a full dashboard — workloads,
configmaps, events, logs, custom resources — and the terminal is one
feature among many. Porthole is the terminal. Headlamp authorizes
via Kubernetes RBAC (typically using user impersonation against the
kube-apiserver); Porthole authorizes via OPA on its own action
vocabulary (`inject_ec`, `attach_ec`, etc.) before it touches the
kube API, so you can express things k8s RBAC cannot — "only during
business hours", "only on namespaces labeled `tier=staging`",
group-to-role bindings that don't require a ClusterRoleBinding per
team. Headlamp's terminal uses the pod's existing shell; Porthole
injects an ephemeral container.

**Pick Headlamp when** you want a general dashboard for your team
and the occasional `bash` into a running pod is enough. **Pick
Porthole when** the dashboard isn't the point, you need richer
authorization than k8s RBAC, or you regularly debug pods with no
shell of their own.

## Lens / OpenLens

[k8slens.dev](https://k8slens.dev/) — desktop "Kubernetes IDE" from
Mirantis. **Lens** is the commercial product (closed-source EULA,
sign-in required, paid tiers). **OpenLens** is the MIT-licensed
unbundled core maintained by the community.

**Where it overlaps.** The user-visible job is the same: pick a
cluster, pick a pod, get a terminal in your browser… well, in your
Electron window.

**Where it differs.** Lens runs on your laptop and talks to clusters
through your local kubeconfig. There is no central identity layer,
no central authorization layer, no central audit — every developer
has their own kubeconfig with their own credentials, and the
cluster's k8s RBAC is the only gate. That's fine for small teams or
SREs but doesn't compose with corporate SSO without a kubeconfig
plugin (`kubelogin`, cloud-provider auth helpers, etc.) on every
machine. Porthole inverts this: a single in-cluster service with a
single ServiceAccount, fronted by your OIDC gateway, audited
centrally.

Lens uses `kubectl exec` into the existing container; Porthole
injects an ephemeral container with a custom image.

**Pick Lens/OpenLens when** developers already manage their own
kubeconfigs, you want a rich desktop dashboard, and central audit
isn't a requirement. **Pick Porthole when** you don't want every
developer to hold long-lived cluster credentials, you need one place
to audit who attached to what, or you want to debug pods that ship
without a shell.

## When to pick Porthole

In one line: **you already have an OIDC gateway, you want one
central audit trail of pod attaches, and you regularly need to
debug pods that don't ship with their own shell.**

If any of those three is false, one of the projects above is
probably a better fit.
