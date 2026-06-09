package auth

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig is read from env in NewJWTMiddlewareFromEnv.
type JWTConfig struct {
	// JWKSURL is the JWKS endpoint (e.g. .../protocol/openid-connect/certs).
	JWKSURL string
	// Issuer is the expected `iss` claim; empty disables the check.
	Issuer string
	// Audience is the expected `aud` claim; empty disables the check.
	Audience string
}

// NewJWTMiddleware returns a gin middleware that validates a bearer
// token against the JWKS and stamps a *Principal onto the context.
//
// Token discovery order: X-ID-Token header (per Envoy Gateway's planned
// forwardIDToken), then Authorization: Bearer <token>.
//
// When AUTH_DISABLED=true the middleware short-circuits and stamps a
// "local-dev" principal so handlers downstream behave consistently.
func NewJWTMiddleware(cfg JWTConfig) (gin.HandlerFunc, error) {
	if os.Getenv("AUTH_DISABLED") == "true" {
		return func(c *gin.Context) {
			c.Set(principalKey, &Principal{
				Sub:    "local-dev",
				Groups: []string{"local-dev"},
			})
			c.Set("user", "local-dev")
			c.Next()
		}, nil
	}
	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("JWKSURL required when AUTH_DISABLED is not set")
	}
	k, err := keyfunc.NewDefault([]string{cfg.JWKSURL})
	if err != nil {
		return nil, fmt.Errorf("init JWKS: %w", err)
	}
	return func(c *gin.Context) {
		raw := bearer(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		parsed, err := jwt.Parse(raw, k.Keyfunc,
			jwt.WithLeeway(30*time.Second),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !parsed.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid token: " + errMsg(err),
			})
			return
		}
		claims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unreadable claims"})
			return
		}
		if cfg.Issuer != "" {
			if iss, _ := claims["iss"].(string); iss != cfg.Issuer {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "issuer mismatch",
				})
				return
			}
		}
		if cfg.Audience != "" {
			if !audienceMatches(claims["aud"], cfg.Audience) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "audience mismatch",
				})
				return
			}
		}
		p := &Principal{
			Sub:               str(claims["sub"]),
			Email:             str(claims["email"]),
			Groups:            groups(claims),
			PreferredUsername: str(claims["preferred_username"]),
			Name:              str(claims["name"]),
			GivenName:         str(claims["given_name"]),
			FamilyName:        str(claims["family_name"]),
			Raw:               raw,
		}
		c.Set(principalKey, p)
		c.Set("user", p.Sub)
		c.Next()
	}, nil
}

func bearer(c *gin.Context) string {
	if h := c.GetHeader("X-ID-Token"); h != "" {
		return h
	}
	if tok, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer "); ok {
		return tok
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// groups reads the `groups` claim, falling back to Keycloak's default
// `realm_access.roles` when the realm hasn't been configured to put a
// groups mapper in the token.
func groups(c jwt.MapClaims) []string {
	if g := strSlice(c["groups"]); len(g) > 0 {
		return g
	}
	ra, _ := c["realm_access"].(map[string]any)
	return strSlice(ra["roles"])
}

func strSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func audienceMatches(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func errMsg(e error) string {
	if e == nil {
		return "validation failed"
	}
	return e.Error()
}

// Ensure ErrInvalidKey is referenced so go vet doesn't complain if jwt is
// otherwise only used via Parse.
var _ = errors.New
