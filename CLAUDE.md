# CLAUDE.md (porthole)

Operational notes for working with this repo. Not exhaustive — the
public-facing docs live in `README.md`.

## Day-to-day demo cluster

- `make -C docs/private demo-up` brings up `porthole-demo-v2` (the
  recording-shaped stack with `team-a-prod`, `inventory`, etc.).
  `make -C docs/private e2e-up` brings up `porthole-e2e` (generic
  `ns-a`/`ns-b`, full Envoy Gateway + OIDC + ExternalDNS).
- Everything under `docs/private/` is gitignored. Scripts there are
  for the author's local workflow.
- marina-managed kind clusters use the bare `$CLUSTER` context name
  (no `kind-` prefix). `kubectl --context porthole-demo-v2 ...`.
- `docker exec` into kind nodes needs `eval "$(marina docker-env)"`
  first — the docker daemon lives inside the Lima VM, not on the
  host.
- Both scripts share the cached OIDC client secret at
  `docs/private/scripts/.porthole-client-secret`. Don't split it
  per-script — `kc client create` silent-409s on the second to run
  and the two scripts' kube Secrets would diverge from Keycloak.

## Releases

- Chart.yaml version bump on `main` triggers
  `.github/workflows/chart-release.yml`: builds the multi-arch
  porthole image with `ko`, pushes both chart + image to GHCR,
  tags `chart-v<version>`. Patch is the default; minor on
  substantive feature batches.
- Helm chart: `oci://ghcr.io/bcollard/charts/porthole`.
- Image: `ghcr.io/bcollard/porthole`.
- The "Set package visibility to public" step warns harmlessly when
  the GH user-packages API 404s on the visibility endpoint — the
  package is usually already public from an earlier publish. The
  warning URLs in the workflow output are the correct settings
  pages if you ever need to flip manually.

## Audit log

ECS v8.11. Don't drift — the marketing site at
`porthole.runlocal.dev/#audit` and the README's audit section both
key off this shape. Canonical per-variant payload lives in
`pkg/audit/audit_smoke_test.go`; `go test -v ./pkg/audit/`
pretty-prints one of each event type.

## SPA

The SPA at `pkg/web/dist/` is embedded into the Go binary via
`go:embed`. Edit, then `go build` to re-embed — no separate frontend
build step.

Browser caching note: `embed.FS` modtimes are the zero value, so
`Last-Modified` looks "never changed" to browsers. After rolling
the image, **hard-reload** (Cmd-Shift-R / Ctrl-Shift-R) to bypass
cache. This causes confusion almost every time something looks
broken in the UI after a re-roll.

## EC cleanup signal

`pkg/ephemeral/cleanup-ec.go` uses **SIGHUP**, not SIGKILL or
SIGTERM. Per `pid_namespaces(7)`, signals sent to PID 1 from inside
its own PID namespace are silently dropped unless PID 1 has a
registered handler — SIGKILL/SIGSTOP can never have handlers and
get dropped. Interactive shells (zsh, bash) install a SIGHUP
handler and exit cleanly on it. Don't "harden" this to SIGKILL —
it'll regress to silent no-ops.

## Known foot-guns

- **bash auto-sets `HOSTNAME`** in non-interactive scripts, so
  `${HOSTNAME:-default}` doesn't fall through. Use a different
  variable name. The two `docs/private/scripts/*.sh` files use
  `PUBLIC_HOSTNAME` for this reason.
- **Helm OCI push to `oci://ghcr.io/<owner>/charts`** produces a
  GHCR package named `charts/<chart>` (URL-encoded
  `charts%2F<chart>`), not `<chart>`. The visibility URL paths
  reflect that. The container image lives at the plain `<chart>`
  package — no `charts/` prefix.
- **TargetContainerName on an injected EC** makes the EC share the
  target container's PID namespace; PID 1 in the EC's POV becomes
  the target's main process. Convenient for `ps`-into-the-app
  debugging, but lethal for cleanup if the kill mechanism touches
  PID 1. We deliberately do NOT set this — see comment in
  `pkg/ephemeral/list-create-ec.go`.

## Tests

- `make opa-eval` — 15-case Rego sanity check, no cluster required.
- `go test ./...` — Go test suite. `pkg/audit/audit_smoke_test.go`
  is verification-by-eyeball (prints sample lines).
- No end-to-end test harness; the demo scripts under `docs/private/`
  are the integration test.
