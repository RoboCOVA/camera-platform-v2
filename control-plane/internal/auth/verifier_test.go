package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yourorg/cam-platform/internal/auth"
)

// ─── Test key pair ────────────────────────────────────────────────────────────

type testKeyPair struct {
	kid string
	key *rsa.PrivateKey
}

func newTestKeyPair(t testing.TB) *testKeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &testKeyPair{kid: fmt.Sprintf("kid-%d", time.Now().UnixNano()), key: key}
}

func (kp *testKeyPair) jwksServer(t testing.TB) *httptest.Server {
	t.Helper()
	nBytes := kp.key.PublicKey.N.Bytes()
	eBytes := new(big.Int).SetInt64(int64(kp.key.PublicKey.E)).Bytes()
	jwks := map[string]interface{}{
		"keys": []map[string]interface{}{
			{"kid": kp.kid, "kty": "RSA", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(nBytes),
				"e": base64.RawURLEncoding.EncodeToString(eBytes)},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (kp *testKeyPair) sign(t testing.TB, issuer string, claims map[string]interface{}) string {
	t.Helper()
	now := time.Now()
	base := map[string]interface{}{
		"sub": "user-123", "iss": issuer, "aud": "cam-api",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"email": "test@example.com", "org_id": "org-test", "roles": []string{"viewer"},
	}
	for k, v := range claims {
		base[k] = v
	}
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kp.kid})
	pay, _ := json.Marshal(base)
	h64 := base64.RawURLEncoding.EncodeToString(hdr)
	p64 := base64.RawURLEncoding.EncodeToString(pay)
	input := h64 + "." + p64
	hash := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, kp.key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func newVerifier(t testing.TB, kp *testKeyPair) (*httptest.Server, *auth.Verifier) {
	t.Helper()
	srv := kp.jwksServer(t)
	issuer := srv.URL + "/realms/camplatform"
	v := auth.NewVerifierWithOptions(auth.Options{
		JWKSURL: srv.URL + "/certs", Issuer: issuer, Audience: "cam-api",
	})
	return srv, v
}

// ─── Verifier tests ───────────────────────────────────────────────────────────

func TestVerify_ValidToken(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"

	token := kp.sign(t, issuer, map[string]interface{}{
		"org_id": "org-abc", "roles": []string{"org-admin"}, "email": "alice@example.com",
	})

	claims, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims.OrgID != "org-abc" {
		t.Errorf("org_id: got %q, want %q", claims.OrgID, "org-abc")
	}
	if !claims.HasRole("org-admin") {
		t.Error("expected org-admin role")
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	past := time.Now().Add(-10 * time.Minute)
	token := kp.sign(t, issuer, map[string]interface{}{
		"iat": past.Unix(), "exp": past.Add(5 * time.Minute).Unix(), "org_id": "org-abc",
	})
	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerify_WrongIssuer(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	_ = srv
	token := kp.sign(t, "https://wrong-issuer.com/realms/x", map[string]interface{}{"org_id": "org-abc"})
	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestVerify_WrongAudience(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	token := kp.sign(t, issuer, map[string]interface{}{"aud": "wrong-service", "org_id": "org-abc"})
	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
}

func TestVerify_MissingOrgID(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	token := kp.sign(t, issuer, map[string]interface{}{"org_id": ""})
	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for missing org_id")
	}
}

func TestVerify_TamperedSignature(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	token := kp.sign(t, issuer, map[string]interface{}{"org_id": "org-abc"})
	parts := [3]string{}
	i := 0
	for _, p := range [3]string{token[:10], token[10:20], token[20:]} {
		parts[i] = p
		i++
	}
	// simple tamper: flip last byte of full token sig part
	runes := []rune(token)
	runes[len(runes)-1] ^= 1
	tampered := string(runes)
	_, err := v.Verify(context.Background(), tampered)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestVerify_MalformedTokens(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	_ = srv
	for _, bad := range []string{"", "only-one-part", "two.parts", "a.b.c.d"} {
		if _, err := v.Verify(context.Background(), bad); err == nil {
			t.Errorf("expected error for %q, got nil", bad)
		}
	}
}

// ─── Claims tests ─────────────────────────────────────────────────────────────

func TestClaims_HasRole(t *testing.T) {
	c := &auth.Claims{Roles: []string{"org-admin", "viewer"}}
	if !c.HasRole("org-admin") { t.Error("expected org-admin") }
	if c.HasRole("org-owner") { t.Error("unexpected org-owner") }
}

func TestClaims_IsAtLeastAdmin(t *testing.T) {
	cases := []struct{ roles []string; want bool }{
		{[]string{"org-owner"}, true},
		{[]string{"org-admin"}, true},
		{[]string{"viewer"}, false},
		{[]string{}, false},
	}
	for _, c := range cases {
		got := (&auth.Claims{Roles: c.roles}).IsAtLeastAdmin()
		if got != c.want {
			t.Errorf("roles=%v: got %v, want %v", c.roles, got, c.want)
		}
	}
}

func TestClaims_CanAccessSite(t *testing.T) {
	c := &auth.Claims{SiteIDs: nil}
	if !c.CanAccessSite("any") { t.Error("expected access with no restriction") }
	c2 := &auth.Claims{SiteIDs: []string{"site-a"}}
	if !c2.CanAccessSite("site-a") { t.Error("expected access to site-a") }
	if c2.CanAccessSite("site-b") { t.Error("unexpected access to site-b") }
}

// ─── Middleware tests ─────────────────────────────────────────────────────────

func TestMiddleware_MissingHeader(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	_ = srv
	handler := auth.Middleware(v)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 { t.Errorf("want 401, got %d", rec.Code) }
}

func TestMiddleware_ValidToken_InjectsContext(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	var got *auth.Claims
	handler := auth.Middleware(v)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = auth.ClaimsFromContext(r.Context())
		w.WriteHeader(200)
	}))
	token := kp.sign(t, issuer, map[string]interface{}{"org_id": "org-xyz", "email": "bob@x.com"})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 { t.Fatalf("want 200, got %d", rec.Code) }
	if got == nil { t.Fatal("claims not injected") }
	if got.OrgID != "org-xyz" { t.Errorf("org_id: %q", got.OrgID) }
}

func TestMiddleware_QueryParamToken(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	handler := auth.Middleware(v)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	token := kp.sign(t, issuer, map[string]interface{}{"org_id": "org-abc"})
	req := httptest.NewRequest("GET", "/?token="+token, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 { t.Errorf("want 200 for ?token=, got %d", rec.Code) }
}

func TestRequireRole(t *testing.T) {
	kp := newTestKeyPair(t)
	srv, v := newVerifier(t, kp)
	issuer := srv.URL + "/realms/camplatform"
	handler := auth.Middleware(v)(auth.RequireRole("org-admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	))
	cases := []struct{ roles []string; want int }{
		{[]string{"org-admin"}, 200},
		{[]string{"viewer"}, 403},
		{[]string{}, 403},
	}
	for _, c := range cases {
		token := kp.sign(t, issuer, map[string]interface{}{"org_id": "org-abc", "roles": c.roles})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != c.want { t.Errorf("roles=%v: got %d, want %d", c.roles, rec.Code, c.want) }
	}
}

// ─── Benchmark ────────────────────────────────────────────────────────────────

func BenchmarkVerify_CacheHit(b *testing.B) {
	kp := newTestKeyPair(b)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	kp.key = key
	srv := kp.jwksServer(b)
	issuer := srv.URL + "/realms/camplatform"
	v := auth.NewVerifierWithOptions(auth.Options{
		JWKSURL: srv.URL + "/certs", Issuer: issuer, Audience: "cam-api",
		CacheTTL: time.Hour,
	})
	token := kp.sign(b, issuer, map[string]interface{}{"org_id": "org-bench"})
	v.Verify(context.Background(), token) // warm cache
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			v.Verify(context.Background(), token)
		}
	})
}
