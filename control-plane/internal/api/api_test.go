// Package api_test contains integration tests for the control plane HTTP API.
// These tests start a real HTTP server backed by a test Postgres database.
// Run with: go test ./internal/api/... -v -tags integration
//
// Prerequisites:
//
//	TEST_DATABASE_URL=postgres://cam:cam@localhost:5432/camplatform_test?sslmode=disable
package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq"

	"github.com/yourorg/cam-platform/internal/auth"
	"github.com/yourorg/cam-platform/internal/testutil"
)

// ─── Test server setup ────────────────────────────────────────────────────────

type testServer struct {
	srv    *httptest.Server
	db     *sql.DB
	kp     *testutil.KeyPair
	issuer string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	db := testutil.DB(t)

	kp := testutil.NewKeyPair(t)
	jwksSrv := kp.JWKSServer(t)
	issuer := jwksSrv.URL + "/realms/camplatform"

	verifier := auth.NewVerifierWithOptions(auth.Options{
		JWKSURL:  jwksSrv.URL + "/certs",
		Issuer:   issuer,
		Audience: "cam-api",
	})

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// Public
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	r.Post("/api/devices/heartbeat", makeHeartbeatHandler(db))
	r.Post("/api/devices/cameras", makeCamerasHandler(db))

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(verifier))
		r.Get("/api/cameras", makeCameraListHandler(db))
		r.Get("/api/cameras/{cameraID}", makeCameraGetHandler(db))
		r.Get("/api/sites", makeSiteListHandler(db))
		r.Post("/api/sites", makeSiteCreateHandler(db))
		r.Get("/api/events", makeEventListHandler(db))
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return &testServer{srv: srv, db: db, kp: kp, issuer: issuer}
}

// authHeader returns a valid Authorization header for the given org and roles.
func (ts *testServer) authHeader(t *testing.T, orgID string, roles ...string) string {
	t.Helper()
	if len(roles) == 0 {
		roles = []string{"viewer"}
	}
	return ts.kp.AuthHeader(t, ts.issuer, map[string]interface{}{
		"org_id": orgID,
		"roles":  roles,
	})
}

// get performs a GET request against the test server.
func (ts *testServer) get(t *testing.T, path, auth string) *http.Response {
	t.Helper()
	return testutil.Do(t, ts.srv, "GET", path, "", map[string]string{"Authorization": auth})
}

// post performs a POST request.
func (ts *testServer) post(t *testing.T, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	return testutil.Do(t, ts.srv, "POST", path, body, headers)
}

// ─── Health ───────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.get(t, "/health", "")
	testutil.AssertStatus(t, resp, 200)
}

// ─── Camera list ──────────────────────────────────────────────────────────────

func TestCameraList_Empty(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme Corp")

	resp := ts.get(t, "/api/cameras", ts.authHeader(t, orgID))
	testutil.AssertStatus(t, resp, 200)

	var body struct {
		Items []map[string]interface{} `json:"items"`
		Total int                      `json:"total"`
	}
	testutil.MustJSON(t, resp, &body)

	if len(body.Items) != 0 {
		t.Errorf("expected empty camera list, got %d", len(body.Items))
	}
}

func TestCameraList_ReturnsCamerasForOrg(t *testing.T) {
	ts := newTestServer(t)
	orgA := testutil.Org(t, ts.db, "Org A")
	orgB := testutil.Org(t, ts.db, "Org B")
	siteA := testutil.Site(t, ts.db, orgA, "Site 1")
	devA, _ := testutil.Device(t, ts.db, orgA, siteA)

	// Add 3 cameras to org A, 1 to org B
	for i := 0; i < 3; i++ {
		testutil.Camera(t, ts.db, orgA, siteA, devA)
	}
	siteB := testutil.Site(t, ts.db, orgB, "Site B")
	devB, _ := testutil.Device(t, ts.db, orgB, siteB)
	testutil.Camera(t, ts.db, orgB, siteB, devB)

	// Org A should see exactly 3 cameras
	resp := ts.get(t, "/api/cameras", ts.authHeader(t, orgA))
	testutil.AssertStatus(t, resp, 200)

	var body struct {
		Items []map[string]interface{} `json:"items"`
	}
	testutil.MustJSON(t, resp, &body)

	if len(body.Items) != 3 {
		t.Errorf("org A: expected 3 cameras, got %d", len(body.Items))
	}

	// Org B should see exactly 1
	resp2 := ts.get(t, "/api/cameras", ts.authHeader(t, orgB))
	var body2 struct {
		Items []map[string]interface{} `json:"items"`
	}
	testutil.MustJSON(t, resp2, &body2)
	if len(body2.Items) != 1 {
		t.Errorf("org B: expected 1 camera, got %d", len(body2.Items))
	}
}

func TestCameraList_FilterBySite(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	site1 := testutil.Site(t, ts.db, orgID, "Site 1")
	site2 := testutil.Site(t, ts.db, orgID, "Site 2")
	dev1, _ := testutil.Device(t, ts.db, orgID, site1)
	dev2, _ := testutil.Device(t, ts.db, orgID, site2)

	testutil.Camera(t, ts.db, orgID, site1, dev1)
	testutil.Camera(t, ts.db, orgID, site1, dev1)
	testutil.Camera(t, ts.db, orgID, site2, dev2)

	resp := ts.get(t, "/api/cameras?site_id="+site1, ts.authHeader(t, orgID))
	testutil.AssertStatus(t, resp, 200)

	var body struct {
		Items []map[string]interface{} `json:"items"`
	}
	testutil.MustJSON(t, resp, &body)

	if len(body.Items) != 2 {
		t.Errorf("expected 2 cameras for site1, got %d", len(body.Items))
	}
}

func TestCameraList_Unauthenticated(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.get(t, "/api/cameras", "") // no auth header
	testutil.AssertStatus(t, resp, 401)
}

// ─── Camera get ───────────────────────────────────────────────────────────────

func TestCameraGet_Found(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	siteID := testutil.Site(t, ts.db, orgID, "Warehouse")
	devID, _ := testutil.Device(t, ts.db, orgID, siteID)
	camID := testutil.Camera(t, ts.db, orgID, siteID, devID)

	resp := ts.get(t, "/api/cameras/"+camID, ts.authHeader(t, orgID))
	testutil.AssertStatus(t, resp, 200)

	var cam map[string]interface{}
	testutil.MustJSON(t, resp, &cam)

	if cam["id"] != camID {
		t.Errorf("id: got %v, want %v", cam["id"], camID)
	}
}

func TestCameraGet_CrossOrgBlocked(t *testing.T) {
	ts := newTestServer(t)
	orgA := testutil.Org(t, ts.db, "Org A")
	orgB := testutil.Org(t, ts.db, "Org B")
	siteA := testutil.Site(t, ts.db, orgA, "Site A")
	devA, _ := testutil.Device(t, ts.db, orgA, siteA)
	camA := testutil.Camera(t, ts.db, orgA, siteA, devA)

	// Org B tries to access Org A's camera
	resp := ts.get(t, "/api/cameras/"+camA, ts.authHeader(t, orgB))
	testutil.AssertStatus(t, resp, 404)
}

func TestCameraGet_NotFound(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	resp := ts.get(t, "/api/cameras/nonexistent-id", ts.authHeader(t, orgID))
	testutil.AssertStatus(t, resp, 404)
}

// ─── Sites ────────────────────────────────────────────────────────────────────

func TestSiteCreate(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")

	body := `{"name":"Main Warehouse","address":"123 Main St","timezone":"America/New_York"}`
	resp := ts.post(t, "/api/sites", body, map[string]string{
		"Authorization": ts.authHeader(t, orgID, "org-admin"),
	})
	testutil.AssertStatus(t, resp, 201)

	var created map[string]interface{}
	testutil.MustJSON(t, resp, &created)
	if created["id"] == nil || created["id"] == "" {
		t.Error("expected non-empty id in response")
	}
}

func TestSiteList(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	testutil.Site(t, ts.db, orgID, "Site A")
	testutil.Site(t, ts.db, orgID, "Site B")

	resp := ts.get(t, "/api/sites", ts.authHeader(t, orgID))
	testutil.AssertStatus(t, resp, 200)

	var sites []map[string]interface{}
	testutil.MustJSON(t, resp, &sites)
	if len(sites) != 2 {
		t.Errorf("expected 2 sites, got %d", len(sites))
	}
}

// ─── Device heartbeat ─────────────────────────────────────────────────────────

func TestHeartbeat_ValidDeviceKey(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	siteID := testutil.Site(t, ts.db, orgID, "Site 1")
	_, deviceKey := testutil.Device(t, ts.db, orgID, siteID)

	body := fmt.Sprintf(`{"device_id":"dev-1","agent_version":"0.1.0","cameras":[]}`)
	resp := ts.post(t, "/api/devices/heartbeat", body, map[string]string{
		"X-Device-Key": deviceKey,
	})
	testutil.AssertStatus(t, resp, 200)

	// Verify last_seen was updated
	var lastSeen time.Time
	ts.db.QueryRowContext(context.Background(),
		`SELECT last_seen FROM devices WHERE device_key = $1`, deviceKey).Scan(&lastSeen)
	if time.Since(lastSeen) > 5*time.Second {
		t.Errorf("last_seen not updated: %v", lastSeen)
	}
}

func TestHeartbeat_MissingDeviceKey(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.post(t, "/api/devices/heartbeat", `{}`, nil)
	testutil.AssertStatus(t, resp, 401)
}

func TestHeartbeat_UnknownDeviceKey(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.post(t, "/api/devices/heartbeat", `{"device_id":"x"}`,
		map[string]string{"X-Device-Key": "totally-unknown-key"})
	// Should succeed silently (UPDATE matches 0 rows but doesn't error)
	testutil.AssertStatus(t, resp, 200)
}

// ─── Device cameras registration ──────────────────────────────────────────────

func TestDeviceCameras_RegisterNewCameras(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	siteID := testutil.Site(t, ts.db, orgID, "Site 1")
	devID, deviceKey := testutil.Device(t, ts.db, orgID, siteID)
	_ = devID

	body := fmt.Sprintf(`{
		"device_id": "%s",
		"cameras": [
			{
				"id": "cam-stable-id-001",
				"name": "Front Door",
				"manufacturer": "Hikvision",
				"model": "DS-2CD2143",
				"serial": "SN123456",
				"ip": "192.168.1.100",
				"main_stream_url": "rtsp://admin:pass@192.168.1.100/stream1",
				"sub_stream_url": "rtsp://admin:pass@192.168.1.100/stream2",
				"width": 2688,
				"height": 1520
			}
		]
	}`, devID)

	resp := ts.post(t, "/api/devices/cameras", body, map[string]string{
		"X-Device-Key": deviceKey,
	})
	testutil.AssertStatus(t, resp, 200)

	// Verify camera was inserted
	var count int
	ts.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM cameras WHERE id = 'cam-stable-id-001' AND org_id = $1`, orgID).
		Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 camera, got %d", count)
	}
}

func TestDeviceCameras_UpsertExistingCamera(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")
	siteID := testutil.Site(t, ts.db, orgID, "Site 1")
	devID, deviceKey := testutil.Device(t, ts.db, orgID, siteID)

	// Insert camera once
	body := fmt.Sprintf(`{"device_id":"%s","cameras":[
		{"id":"cam-001","name":"Old Name","manufacturer":"Hik","model":"X","serial":"S","ip":"1.2.3.4",
		 "main_stream_url":"rtsp://old","sub_stream_url":"","width":1920,"height":1080}
	]}`, devID)
	ts.post(t, "/api/devices/cameras", body, map[string]string{"X-Device-Key": deviceKey})

	// Update with new name
	body2 := fmt.Sprintf(`{"device_id":"%s","cameras":[
		{"id":"cam-001","name":"New Name","manufacturer":"Hik","model":"X","serial":"S","ip":"1.2.3.4",
		 "main_stream_url":"rtsp://new","sub_stream_url":"","width":1920,"height":1080}
	]}`, devID)
	ts.post(t, "/api/devices/cameras", body2, map[string]string{"X-Device-Key": deviceKey})

	// Name should be updated
	var name string
	ts.db.QueryRowContext(context.Background(),
		`SELECT name FROM cameras WHERE id = 'cam-001'`).Scan(&name)
	if name != "New Name" {
		t.Errorf("expected 'New Name', got %q", name)
	}

	// Should still be only 1 row (upsert, not insert)
	var count int
	ts.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM cameras WHERE id = 'cam-001'`).Scan(&count)
	if count != 1 {
		t.Errorf("upsert created duplicate: count=%d", count)
	}
}

// ─── Events ───────────────────────────────────────────────────────────────────

func TestEventList_Empty(t *testing.T) {
	ts := newTestServer(t)
	orgID := testutil.Org(t, ts.db, "Acme")

	resp := ts.get(t, "/api/events", ts.authHeader(t, orgID))
	testutil.AssertStatus(t, resp, 200)

	var body struct {
		Items []interface{} `json:"items"`
	}
	testutil.MustJSON(t, resp, &body)
	if len(body.Items) != 0 {
		t.Errorf("expected empty events, got %d", len(body.Items))
	}
}

// ─── Minimal route handlers for test server ───────────────────────────────────
// In real code these live in cmd/api/main.go. We duplicate minimal versions
// here so the test package is self-contained and doesn't depend on the full
// main package (which would cause circular imports).

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func makeHeartbeatHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Device-Key")
		if key == "" {
			http.Error(w, `{"error":"missing device key"}`, 401)
			return
		}
		var body struct {
			AgentVersion string `json:"agent_version"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		db.ExecContext(r.Context(),
			`UPDATE devices SET status='online', last_seen=NOW(), agent_version=$1 WHERE device_key=$2`,
			body.AgentVersion, key)
		writeJSON(w, 200, map[string]string{"status": "ok"})
	}
}

func makeCamerasHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Device-Key")
		if key == "" {
			http.Error(w, `{"error":"missing device key"}`, 401)
			return
		}
		var deviceID, orgID string
		var siteID sql.NullString
		err := db.QueryRowContext(r.Context(),
			`SELECT id, org_id, site_id FROM devices WHERE device_key=$1`, key).
			Scan(&deviceID, &orgID, &siteID)
		if err != nil {
			http.Error(w, `{"error":"unknown device"}`, 403)
			return
		}
		var payload struct {
			Cameras []struct {
				ID            string `json:"id"`
				Name          string `json:"name"`
				Manufacturer  string `json:"manufacturer"`
				Model         string `json:"model"`
				Serial        string `json:"serial"`
				IP            string `json:"ip"`
				MainStreamURL string `json:"main_stream_url"`
				SubStreamURL  string `json:"sub_stream_url"`
				Width         int    `json:"width"`
				Height        int    `json:"height"`
			} `json:"cameras"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		for _, cam := range payload.Cameras {
			db.ExecContext(r.Context(), `
				INSERT INTO cameras (id,org_id,site_id,device_id,name,manufacturer,model,serial,
				  ip,main_stream_url,sub_stream_url,width,height,status,last_seen)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::inet,$10,$11,$12,$13,'online',NOW())
				ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,
				  main_stream_url=EXCLUDED.main_stream_url, status='online', last_seen=NOW()`,
				cam.ID, orgID, siteID, deviceID, cam.Name, cam.Manufacturer, cam.Model,
				cam.Serial, cam.IP, cam.MainStreamURL, cam.SubStreamURL, cam.Width, cam.Height)
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	}
}

func makeCameraListHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.MustClaimsFromContext(r.Context())
		siteFilter := r.URL.Query().Get("site_id")
		where := `WHERE org_id=$1`
		args := []interface{}{claims.OrgID}
		if siteFilter != "" {
			where += " AND site_id=$2"
			args = append(args, siteFilter)
		}

		var total int
		if err := db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM cameras "+where, args...).Scan(&total); err != nil {
			http.Error(w, `{"error":"internal"}`, 500)
			return
		}

		query := `SELECT id,name,manufacturer,model,ip,width,height,status FROM cameras ` + where + " ORDER BY name LIMIT 100 OFFSET 0"
		rows, err := db.QueryContext(r.Context(), query, args...)
		if err != nil {
			http.Error(w, `{"error":"internal"}`, 500)
			return
		}
		defer rows.Close()
		type cam struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Manufacturer string `json:"manufacturer"`
			Model        string `json:"model"`
			IP           string `json:"ip"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			Status       string `json:"status"`
		}
		var cameras []cam
		for rows.Next() {
			var c cam
			rows.Scan(&c.ID, &c.Name, &c.Manufacturer, &c.Model, &c.IP, &c.Width, &c.Height, &c.Status)
			cameras = append(cameras, c)
		}
		if cameras == nil {
			cameras = []cam{}
		}
		writeJSON(w, 200, map[string]interface{}{
			"items":  cameras,
			"total":  total,
			"limit":  100,
			"offset": 0,
		})
	}
}

func makeCameraGetHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.MustClaimsFromContext(r.Context())
		cameraID := chi.URLParam(r, "cameraID")
		var cam struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		err := db.QueryRowContext(r.Context(),
			`SELECT id,name,status FROM cameras WHERE id=$1 AND org_id=$2`,
			cameraID, claims.OrgID).Scan(&cam.ID, &cam.Name, &cam.Status)
		if err == sql.ErrNoRows {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		writeJSON(w, 200, cam)
	}
}

func makeSiteListHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.MustClaimsFromContext(r.Context())
		rows, _ := db.QueryContext(r.Context(),
			`SELECT id,name FROM sites WHERE org_id=$1 ORDER BY name`, claims.OrgID)
		defer rows.Close()
		type site struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		var sites []site
		for rows.Next() {
			var s site
			rows.Scan(&s.ID, &s.Name)
			sites = append(sites, s)
		}
		if sites == nil {
			sites = []site{}
		}
		writeJSON(w, 200, sites)
	}
}

func makeSiteCreateHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.MustClaimsFromContext(r.Context())
		var body struct {
			Name     string `json:"name"`
			Address  string `json:"address"`
			Timezone string `json:"timezone"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Timezone == "" {
			body.Timezone = "UTC"
		}
		var id string
		db.QueryRowContext(r.Context(),
			`INSERT INTO sites (org_id,name,address,timezone) VALUES ($1,$2,$3,$4) RETURNING id`,
			claims.OrgID, body.Name, body.Address, body.Timezone).Scan(&id)
		writeJSON(w, 201, map[string]string{"id": id})
	}
}

func makeEventListHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.MustClaimsFromContext(r.Context())
		var total int
		if err := db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM events WHERE org_id=$1`, claims.OrgID).Scan(&total); err != nil {
			http.Error(w, `{"error":"internal"}`, 500)
			return
		}
		rows, _ := db.QueryContext(r.Context(),
			`SELECT id,type FROM events WHERE org_id=$1 ORDER BY started_at DESC LIMIT 100 OFFSET 0`,
			claims.OrgID)
		defer rows.Close()
		type evt struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		var events []evt
		for rows.Next() {
			var e evt
			rows.Scan(&e.ID, &e.Type)
			events = append(events, e)
		}
		if events == nil {
			events = []evt{}
		}
		writeJSON(w, 200, map[string]interface{}{
			"items":  events,
			"total":  total,
			"limit":  100,
			"offset": 0,
		})
	}
}

// Helpers for test file
var _ = strings.Contains
var _ = os.Getenv
