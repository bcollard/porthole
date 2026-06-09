// Package auth holds the JWT-based authentication and OPA-based
// authorization plumbing for Porthole. The two halves are deliberately
// independent: you can enable either without the other for testing.
package auth

import "github.com/gin-gonic/gin"

const principalKey = "porthole.principal"

// Principal is what the JWT middleware stamps onto the request after
// validation. Other code reads it via PrincipalFromContext.
type Principal struct {
	Sub               string   `json:"sub"`
	Email             string   `json:"email,omitempty"`
	Groups            []string `json:"groups,omitempty"`
	PreferredUsername string   `json:"preferred_username,omitempty"`
	Name              string   `json:"name,omitempty"`
	GivenName         string   `json:"given_name,omitempty"`
	FamilyName        string   `json:"family_name,omitempty"`
	// Raw is the original token. We keep it for debugging; it's never
	// passed downstream (in particular, NOT sent to OPA).
	Raw string `json:"-"`
}

// PrincipalFromContext returns the validated principal, or false when
// the request didn't go through the JWT middleware.
func PrincipalFromContext(c *gin.Context) (*Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return nil, false
	}
	p, ok := v.(*Principal)
	return p, ok
}
