package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
)

// verifyRS256Signature checks PKCS1v15 SHA-256 signature over data.
func verifyRS256Signature(data, sig []byte, key *rsa.PublicKey) error {
	hash := sha256.Sum256(data)
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], sig)
}

type contextKey string
const claimsContextKey contextKey = "auth_claims"

// Middleware validates Bearer JWT tokens and stores *Claims in the request context.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := extractBearerToken(r)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}
			claims, err := v.Verify(r.Context(), token)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "token invalid or expired")
				return
			}
			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole enforces a specific realm role. Must chain after Middleware.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				writeAuthError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			if !claims.HasRole(role) {
				writeAuthError(w, http.StatusForbidden, "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin enforces org-owner or org-admin role.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil || !claims.IsAtLeastAdmin() {
			writeAuthError(w, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClaimsFromContext retrieves *Claims from the request context.
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsContextKey).(*Claims)
	return c
}

// MustClaimsFromContext panics if claims are absent. Use only in auth-protected handlers.
func MustClaimsFromContext(ctx context.Context) *Claims {
	c := ClaimsFromContext(ctx)
	if c == nil {
		panic("auth.MustClaimsFromContext: no claims — is auth.Middleware applied?")
	}
	return c
}

func extractBearerToken(r *http.Request) (string, error) {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		tok := strings.TrimPrefix(auth, "Bearer ")
		if tok == "" {
			return "", errors.New("empty bearer token")
		}
		return tok, nil
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		return tok, nil
	}
	return "", errors.New("missing Authorization: Bearer header")
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="cam-platform"`)
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}
