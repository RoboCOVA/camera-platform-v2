// Package testutil provides shared test helpers for the cam-platform control plane.
// It sets up a real Postgres database (via Docker or a test DSN), a mock JWKS
// server, and pre-built JWT signing helpers so tests can run fully end-to-end
// without mocking at the HTTP layer.
package testutil

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// ─── Database ─────────────────────────────────────────────────────────────────

// DB returns a *sql.DB connected to the test database.
// It reads TEST_DATABASE_URL from the environment, falling back to a
// local Postgres instance. It also runs the schema migrations.
func DB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://cam:cam@localhost:5432/camplatform_test?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("test database not available (%v) — skipping integration test", err)
	}

	// Run migrations
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Register cleanup: truncate all tables after each test
	t.Cleanup(func() {
		truncateAll(t, db)
	})

	return db
}

func runMigrations(db *sql.DB) error {
	schemaPath := "../../deploy/sql/init.sql"
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		// Try alternate path when running from different directories
		schemaPath = "../../../deploy/sql/init.sql"
	}
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		// Schema already applied — skip
		return nil
	}
	_, err = db.Exec(string(data))
	return err
}

func truncateAll(t *testing.T, db *sql.DB) {
	t.Helper()
	tables := []string{
		"audit_log", "provision_tokens", "org_members",
		"alert_rules", "events", "cameras", "devices", "sites", "orgs",
	}
	for _, table := range tables {
		if _, err := db.Exec("TRUNCATE " + table + " CASCADE"); err != nil {
			t.Logf("truncate %s: %v", table, err)
		}
	}
}

// ─── Seed data ────────────────────────────────────────────────────────────────

// Org creates a test organization and returns its ID.
func Org(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var id string
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO orgs (name, slug) VALUES ($1, $2) RETURNING id`,
		name, strings.ToLower(strings.ReplaceAll(name, " ", "-")),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return id
}

// Site creates a test site under the given org and returns its ID.
func Site(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO sites (org_id, name) VALUES ($1, $2) RETURNING id`,
		orgID, name,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed site: %v", err)
	}
	return id
}

// Device creates a test edge device and returns its ID and device key.
func Device(t *testing.T, db *sql.DB, orgID, siteID string) (id, key string) {
	t.Helper()
	key = fmt.Sprintf("test-key-%d", time.Now().UnixNano())
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO devices (org_id, site_id, name, device_key, status, frigate_url)
		 VALUES ($1, $2, 'Test NVR', $3, 'online', 'http://10.10.0.2:5000')
		 RETURNING id`,
		orgID, siteID, key,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return id, key
}

// Camera creates a test camera and returns its ID.
func Camera(t *testing.T, db *sql.DB, orgID, siteID, deviceID string) string {
	t.Helper()
	id := fmt.Sprintf("cam-%d", time.Now().UnixNano())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO cameras
		 (id, org_id, site_id, device_id, name, manufacturer, model, ip,
		  main_stream_url, sub_stream_url, width, height, status, frigate_name)
		 VALUES ($1,$2,$3,$4,'Test Camera','Hikvision','DS-2CD','192.168.1.100',
		         'rtsp://192.168.1.100/stream1','rtsp://192.168.1.100/stream2',
		         1920,1080,'online','hikvision_ds2cd_'||$1)`,
		id, orgID, siteID, deviceID,
	)
	if err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	return id
}

// ─── JWT / auth helpers ───────────────────────────────────────────────────────

// KeyPair holds an RSA key pair for JWT signing in tests.
type KeyPair struct {
	Kid        string
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
}

// NewKeyPair generates a test RSA-2048 key pair.
func NewKeyPair(t *testing.T) *KeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return &KeyPair{
		Kid:        fmt.Sprintf("test-kid-%d", time.Now().UnixNano()),
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
	}
}

// JWKSServer starts a test HTTP server serving the key pair as a JWKS.
// The server URL can be passed to auth.NewVerifierWithOptions as JWKSURL.
func (kp *KeyPair) JWKSServer(t *testing.T) *httptest.Server {
	t.Helper()

	nBytes := kp.PublicKey.N.Bytes()
	eVal := kp.PublicKey.E
	eBig := new(big.Int).SetInt64(int64(eVal))

	jwks := map[string]interface{}{
		"keys": []map[string]interface{}{
			{
				"kid": kp.Kid,
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(nBytes),
				"e":   base64.RawURLEncoding.EncodeToString(eBig.Bytes()),
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Token signs a JWT with the key pair. Claims are merged with sensible defaults.
func (kp *KeyPair) Token(t *testing.T, issuer string, claims map[string]interface{}) string {
	t.Helper()

	now := time.Now()
	base := map[string]interface{}{
		"sub":            "user-test-" + fmt.Sprint(time.Now().UnixNano()),
		"iss":            issuer,
		"aud":            "cam-api",
		"iat":            now.Unix(),
		"exp":            now.Add(5 * time.Minute).Unix(),
		"email":          "test@example.com",
		"email_verified": true,
		"org_id":         "org-test",
		"roles":          []string{"viewer"},
	}
	// Override with caller-supplied claims
	for k, v := range claims {
		base[k] = v
	}

	headerJSON, _ := json.Marshal(map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": kp.Kid,
	})
	claimsJSON, _ := json.Marshal(base)

	h64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	c64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := h64 + "." + c64

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, kp.PrivateKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// AuthHeader returns an Authorization: Bearer header value for the token.
func (kp *KeyPair) AuthHeader(t *testing.T, issuer string, claims map[string]interface{}) string {
	return "Bearer " + kp.Token(t, issuer, claims)
}

// ─── HTTP test helpers ────────────────────────────────────────────────────────

// Do performs an HTTP request against a test server and returns the response.
func Do(t *testing.T, srv *httptest.Server, method, path, body string, headers map[string]string) *http.Response {
	t.Helper()

	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}

	req, err := http.NewRequest(method, srv.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	return resp
}

// MustJSON decodes a JSON response body into v, failing the test on error.
func MustJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}
}

// AssertStatus fails the test if the response status does not match.
func AssertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("status: got %d, want %d", resp.StatusCode, want)
	}
}
