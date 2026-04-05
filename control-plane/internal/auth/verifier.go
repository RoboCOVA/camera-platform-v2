// Package auth provides JWT verification against Keycloak using JWKS.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Claims represents the verified JWT payload including our custom fields.
type Claims struct {
	Subject       string    `json:"sub"`
	IssuedAt      time.Time `json:"iat"`
	ExpiresAt     time.Time `json:"exp"`
	Issuer        string    `json:"iss"`
	Audiences     []string  `json:"aud"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	Name          string    `json:"name"`
	GivenName     string    `json:"given_name"`
	FamilyName    string    `json:"family_name"`
	Username      string    `json:"preferred_username"`
	OrgID         string    `json:"org_id"`
	SiteIDs       []string  `json:"site_ids"`
	Roles         []string  `json:"roles"`
}

func (c *Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func (c *Claims) CanAccessSite(siteID string) bool {
	if len(c.SiteIDs) == 0 {
		return true
	}
	for _, id := range c.SiteIDs {
		if id == siteID {
			return true
		}
	}
	return false
}

func (c *Claims) IsAtLeastAdmin() bool {
	return c.HasRole("org-owner") || c.HasRole("org-admin")
}

// Options configures the Verifier.
type Options struct {
	JWKSURL    string
	Issuer     string
	Audience   string
	CacheTTL   time.Duration
	HTTPClient *http.Client
}

// Verifier validates JWTs against a Keycloak JWKS endpoint.
type Verifier struct {
	opts      Options
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewVerifier creates a Verifier for keycloakURL + realm.
func NewVerifier(keycloakURL, realm string) *Verifier {
	base := strings.TrimRight(keycloakURL, "/")
	issuer := fmt.Sprintf("%s/realms/%s", base, realm)
	return NewVerifierWithOptions(Options{
		JWKSURL:  issuer + "/protocol/openid-connect/certs",
		Issuer:   issuer,
		Audience: "cam-api",
	})
}

// NewVerifierWithOptions creates a Verifier with explicit options (useful in tests).
func NewVerifierWithOptions(opts Options) *Verifier {
	if opts.CacheTTL == 0 {
		opts.CacheTTL = time.Hour
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Verifier{opts: opts, keys: map[string]*rsa.PublicKey{}}
}

// Verify parses and fully validates a raw JWT string.
func (v *Verifier) Verify(ctx context.Context, tokenStr string) (*Claims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token: expected 3 parts, got %d", len(parts))
	}

	// Decode header
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var hdr struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm %q (want RS256)", hdr.Alg)
	}
	if hdr.Kid == "" {
		return nil, fmt.Errorf("missing kid in token header")
	}

	// Get public key
	pubKey, err := v.getKey(ctx, hdr.Kid)
	if err != nil {
		return nil, fmt.Errorf("resolve key %q: %w", hdr.Kid, err)
	}

	// Verify RS256 signature
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if err := verifyRS256Signature([]byte(signingInput), sig, pubKey); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	claims, err := parseClaimsFromJSON(payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	// Validate standard claims
	now := time.Now()
	if now.After(claims.ExpiresAt) {
		return nil, fmt.Errorf("token expired at %s", claims.ExpiresAt.Format(time.RFC3339))
	}
	if !claims.IssuedAt.IsZero() && now.Before(claims.IssuedAt.Add(-30*time.Second)) {
		return nil, fmt.Errorf("token issued in the future (clock skew?)")
	}
	if claims.Issuer != v.opts.Issuer {
		return nil, fmt.Errorf("issuer mismatch: got %q, want %q", claims.Issuer, v.opts.Issuer)
	}

	audOK := false
	for _, aud := range claims.Audiences {
		if aud == v.opts.Audience || aud == "cam-dashboard" || aud == "account" {
			audOK = true
			break
		}
	}
	if !audOK {
		return nil, fmt.Errorf("audience %v does not include %q", claims.Audiences, v.opts.Audience)
	}

	if claims.OrgID == "" {
		return nil, fmt.Errorf("missing org_id claim — user not provisioned to an org")
	}

	return claims, nil
}

func parseClaimsFromJSON(data []byte) (*Claims, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	c := &Claims{}

	getString := func(key string) string {
		v, ok := raw[key]
		if !ok {
			return ""
		}
		var s string
		json.Unmarshal(v, &s)
		return s
	}

	getUnixTime := func(key string) time.Time {
		v, ok := raw[key]
		if !ok {
			return time.Time{}
		}
		var ts int64
		if json.Unmarshal(v, &ts) == nil && ts > 0 {
			return time.Unix(ts, 0)
		}
		return time.Time{}
	}

	c.Subject = getString("sub")
	c.Issuer = getString("iss")
	c.Email = getString("email")
	c.Name = getString("name")
	c.GivenName = getString("given_name")
	c.FamilyName = getString("family_name")
	c.Username = getString("preferred_username")
	c.OrgID = getString("org_id")
	c.IssuedAt = getUnixTime("iat")
	c.ExpiresAt = getUnixTime("exp")

	if v, ok := raw["email_verified"]; ok {
		json.Unmarshal(v, &c.EmailVerified)
	}

	// "aud" can be string or []string per RFC 7519
	if audRaw, ok := raw["aud"]; ok {
		var audArr []string
		var audStr string
		if json.Unmarshal(audRaw, &audArr) == nil {
			c.Audiences = audArr
		} else if json.Unmarshal(audRaw, &audStr) == nil {
			c.Audiences = []string{audStr}
		}
	}

	if v, ok := raw["roles"]; ok {
		json.Unmarshal(v, &c.Roles)
	}
	if v, ok := raw["site_ids"]; ok {
		json.Unmarshal(v, &c.SiteIDs)
	}

	return c, nil
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) getKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// Fast path
	v.mu.RLock()
	if time.Since(v.fetchedAt) < v.opts.CacheTTL {
		if k, ok := v.keys[kid]; ok {
			v.mu.RUnlock()
			return k, nil
		}
	}
	v.mu.RUnlock()

	// Slow path — re-fetch JWKS
	v.mu.Lock()
	defer v.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(v.fetchedAt) < v.opts.CacheTTL {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", v.opts.JWKSURL, nil)
	resp, err := v.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		pk, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		newKeys[k.Kid] = pk
	}

	v.keys = newKeys
	v.fetchedAt = time.Now()

	k, ok := newKeys[kid]
	if !ok {
		return nil, fmt.Errorf("kid %q not found in JWKS (%d keys available)", kid, len(newKeys))
	}
	return k, nil
}

func rsaPublicKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, fmt.Errorf("RSA exponent too large")
	}
	key := &rsa.PublicKey{N: n, E: int(e.Int64())}
	if key.N.BitLen() < 2048 {
		return nil, fmt.Errorf("RSA key too short: %d bits", key.N.BitLen())
	}
	return key, nil
}
