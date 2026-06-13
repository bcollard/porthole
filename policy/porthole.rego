# Porthole authorization policy.
#
# Input shape (from pkg/auth/opa.go):
#   {
#     "input": {
#       "user":             { "sub": "...", "email": "...", "groups": ["..."] },
#       "request":          { "action": "inject_ec", "namespace": "demo", "pod": "target" },
#       "now":              "2026-06-07T14:00:00Z",
#       "namespace_labels": { "team": "a", "tier": "production", ... }
#     }
#   }
#
# A binding constrains the namespace using `namespace_glob`,
# `namespace_labels`, or both. When both are present, BOTH must match
# (logical AND) — this avoids the loose-grant footgun where a glob
# accidentally widens a labels-scoped binding.
#
# Cluster-wide actions (list_namespaces) come with namespace == "" and
# no labels. They are gated by `namespace_glob == "*"` *only* — a
# label-scoped binding cannot grant cluster-wide access.
package porthole.authz

import future.keywords.every
import future.keywords.if
import future.keywords.in

default decision := {"allow": false, "reason": "default deny"}

# effective_bindings returns every binding whose `group` matches one
# of the user's groups, regardless of action/namespace. The SPA reads
# this via /api/me to render the user's role chips in the topbar so a
# logged-in user can see what they're actually allowed to do *before*
# clicking something that gets denied.
#
# Intentionally action-agnostic — listing matched bindings is closer to
# how an operator thinks ("I'm `debugger on team-a-*`") than streaming
# a per-action allow list.
effective_bindings := [b |
	some b in data.policy.bindings
	b.group in input.user.groups
]

# Collect every matching binding via an array comprehension so the
# complete `decision` rule has a single deterministic output even when
# several bindings grant the same request (e.g. team-a is both
# debugger on ns-a and viewer on *). Emitting `decision` once per
# match would trip OPA's eval_conflict_error.
decision := {"allow": true, "reason": concat("; ", reasons)} if {
	reasons := [r |
		some binding in data.policy.bindings
		binding.group in input.user.groups
		matches_namespace(binding, input.request.namespace)
		input.request.action in data.policy.roles[binding.role]
		not violates_time_window(binding)
		r := sprintf("matched: group=%v role=%v ns=%v", [
			binding.group, binding.role, input.request.namespace,
		])
	]
	count(reasons) > 0
}

# Cluster-wide actions: namespace == "". Only an unconditional "*"
# glob covers them; bindings with label constraints do not.
matches_namespace(binding, ns) if {
	ns == ""
	binding.namespace_glob == "*"
	not binding.namespace_labels
}

# Namespaced actions: any specified glob AND any specified labels
# must both hold. A binding with neither (no glob, no labels) is a
# config error and matches nothing.
matches_namespace(binding, ns) if {
	ns != ""
	has_namespace_constraint(binding)
	glob_constraint_met(binding, ns)
	labels_constraint_met(binding)
}

has_namespace_constraint(binding) if binding.namespace_glob
has_namespace_constraint(binding) if binding.namespace_labels

glob_constraint_met(binding, _) if not binding.namespace_glob
glob_constraint_met(binding, ns) if {
	binding.namespace_glob
	glob.match(binding.namespace_glob, [], ns)
}

labels_constraint_met(binding) if not binding.namespace_labels
labels_constraint_met(binding) if {
	binding.namespace_labels
	every k, v in binding.namespace_labels {
		input.namespace_labels[k] == v
	}
}

# A binding may opt in to business-hours-only enforcement.
violates_time_window(binding) if {
	binding.business_hours == true
	weekday_now in {"Saturday", "Sunday"}
}

violates_time_window(binding) if {
	binding.business_hours == true
	hour_now < 9
}

violates_time_window(binding) if {
	binding.business_hours == true
	hour_now >= 17
}

now_ns := time.parse_rfc3339_ns(input.now)

weekday_now := w if {
	w := time.weekday(now_ns)
}

hour_now := h if {
	h := time.clock(now_ns)[0]
}
