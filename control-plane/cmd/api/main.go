package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yourorg/cam-platform/internal/auth"
	"golang.org/x/crypto/curve25519"
)

// ─── Config ───────────────────────────────────────────────────────────────────

type Config struct {
	DatabaseURL    string
	KeycloakURL    string
	KeycloakIssuer string
	KeycloakRealm  string
	MQTTBroker     string
	MQTTUser       string
	MQTTPassword   string
	DeviceSecret   string
	Port           string
	Domain         string
	WGServerPubKey string
	WGContainer    string
	WGInterface    string
}

func configFromEnv() Config {
	return Config{
		DatabaseURL:    mustEnv("DATABASE_URL"),
		KeycloakURL:    mustEnv("KEYCLOAK_URL"),
		KeycloakIssuer: getEnvOr("KEYCLOAK_ISSUER", ""),
		KeycloakRealm:  getEnvOr("KEYCLOAK_REALM", "camplatform"),
		MQTTBroker:     getEnvOr("MQTT_BROKER", "mqtt://localhost:1883"),
		MQTTUser:       os.Getenv("MQTT_USER"),
		MQTTPassword:   os.Getenv("MQTT_PASSWORD"),
		DeviceSecret:   mustEnv("DEVICE_SECRET"),
		Port:           getEnvOr("PORT", "3001"),
		Domain:         mustEnv("DOMAIN"),
		WGServerPubKey: mustEnv("WG_SERVER_PUBLIC_KEY"),
		WGContainer:    getEnvOr("WG_DOCKER_CONTAINER", "cam_wireguard"),
		WGInterface:    getEnvOr("WG_INTERFACE", "wg0"),
	}
}

// ─── App ──────────────────────────────────────────────────────────────────────

type App struct {
	cfg      Config
	db       *sql.DB
	mqtt     mqtt.Client
	hub      *WSHub
	verifier *auth.Verifier
}

func main() {
	cfg := configFromEnv()

	// Connect to Postgres
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	log.Println("[db] connected")

	// MQTT client
	mqttClient := connectMQTT(cfg)
	log.Println("[mqtt] connected")

	// WebSocket hub (fans out MQTT events → browser clients)
	hub := NewWSHub()
	go hub.Run()

	// JWT verifier (Keycloak JWKS)
	jwksURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs",
		strings.TrimRight(cfg.KeycloakURL, "/"), cfg.KeycloakRealm)
	issuer := cfg.KeycloakIssuer
	if issuer == "" {
		issuer = fmt.Sprintf("%s/realms/%s", strings.TrimRight(cfg.KeycloakURL, "/"), cfg.KeycloakRealm)
	}
	verifier := auth.NewVerifierWithOptions(auth.Options{
		JWKSURL:  jwksURL,
		Issuer:   issuer,
		Audience: "cam-api",
	})

	app := &App{cfg: cfg, db: db, mqtt: mqttClient, hub: hub, verifier: verifier}

	// Subscribe to Frigate + ANPR events
	mqttClient.Subscribe("frigate/#", 1, app.handleMQTTEvent)
	mqttClient.Subscribe("anpr/#", 1, app.handleMQTTEvent)

	// Routes
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(corsMiddleware)
	r.Use(metricsMiddleware)
	r.Use(middleware.Timeout(30 * time.Second))

	// Public endpoints
	r.Get("/health", app.handleHealth)
	r.Get("/metrics", promhttp.Handler().ServeHTTP)
	r.Post("/api/devices/heartbeat", app.handleDeviceHeartbeat)
	r.Post("/api/devices/cameras", app.handleDeviceCameras)
	r.Post("/api/provision", app.handleProvision)

	// Authenticated endpoints (JWT required)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(app.verifier))

		// Orgs
		r.Get("/api/orgs/me", app.handleGetMyOrg)

		// Sites
		r.Get("/api/sites", app.handleListSites)
		r.With(auth.RequireAdmin).Post("/api/sites", app.handleCreateSite)
		r.Get("/api/sites/{siteID}", app.handleGetSite)

		// Cameras
		r.Get("/api/cameras", app.handleListCameras)
		r.With(auth.RequireAdmin).Post("/api/cameras", app.handleCreateCamera)
		r.Get("/api/cameras/{cameraID}", app.handleGetCamera)
		r.With(auth.RequireAdmin).Delete("/api/cameras/{cameraID}", app.handleDeleteCamera)
		r.With(auth.RequireAdmin).Patch("/api/cameras/{cameraID}", app.handleUpdateCamera)

		// Events
		r.Get("/api/events", app.handleListEvents)
		r.Get("/api/events/{eventID}", app.handleGetEvent)

		// HLS stream proxy (routes through WireGuard to on-prem Frigate)
		r.Get("/api/stream/{cameraID}/hls/*", app.handleStreamProxy)

		// Frigate snapshot proxy
		r.Get("/api/cameras/{cameraID}/snapshot", app.handleSnapshotProxy)

		// Alert rules
		r.Get("/api/alert-rules", app.handleListAlertRules)
		r.With(auth.RequireAdmin).Post("/api/alert-rules", app.handleCreateAlertRule)
		r.With(auth.RequireAdmin).Delete("/api/alert-rules/{ruleID}", app.handleDeleteAlertRule)

		// Device provisioning tokens
		r.With(auth.RequireAdmin).Post("/api/provision-tokens", app.handleCreateProvisionToken)

		// WebSocket for real-time events
		r.Get("/ws/events", app.handleWebSocket)
	})

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // 0 = no timeout (needed for WebSocket + HLS streaming)
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[api] listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[api] shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
}

// ─── Health ───────────────────────────────────────────────────────────────────

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		http.Error(w, "db unhealthy", 503)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}

// ─── Device endpoints (authenticated by device key, not JWT) ─────────────────

func (a *App) handleDeviceHeartbeat(w http.ResponseWriter, r *http.Request) {
	deviceKey := r.Header.Get("X-Device-Key")
	if deviceKey == "" {
		http.Error(w, "missing device key", 401)
		return
	}

	var payload struct {
		DeviceID string `json:"device_id"`
		Cameras  []struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		} `json:"cameras"`
		AgentVer string `json:"agent_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	_, err := a.db.ExecContext(r.Context(), `
		UPDATE devices
		SET status = 'online', last_seen = NOW(), agent_version = $1
		WHERE device_key = $2`,
		payload.AgentVer, deviceKey)
	if err != nil {
		log.Printf("[heartbeat] db: %v", err)
		http.Error(w, "internal error", 500)
		return
	}

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) handleDeviceCameras(w http.ResponseWriter, r *http.Request) {
	deviceKey := r.Header.Get("X-Device-Key")
	if deviceKey == "" {
		http.Error(w, "missing device key", 401)
		return
	}

	// Fetch device to get org_id and site_id
	var deviceID, orgID string
	var siteID sql.NullString
	err := a.db.QueryRowContext(r.Context(),
		`SELECT id, org_id, site_id FROM devices WHERE device_key = $1`, deviceKey).
		Scan(&deviceID, &orgID, &siteID)
	if err == sql.ErrNoRows {
		http.Error(w, "unknown device", 403)
		return
	}
	if err != nil {
		http.Error(w, "internal error", 500)
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
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	// Upsert each camera
	for _, cam := range payload.Cameras {
		frigateName := sanitizeFrigateName(cam.Name, cam.ID)
		_, err := a.db.ExecContext(r.Context(), `
			INSERT INTO cameras
				(id, org_id, site_id, device_id, name, manufacturer, model, serial,
				 ip, main_stream_url, sub_stream_url, width, height, status, last_seen, frigate_name)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::inet,$10,$11,$12,$13,'online',NOW(),$14)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				manufacturer = EXCLUDED.manufacturer,
				model = EXCLUDED.model,
				serial = EXCLUDED.serial,
				device_id = EXCLUDED.device_id,
				main_stream_url = EXCLUDED.main_stream_url,
				sub_stream_url = EXCLUDED.sub_stream_url,
				ip = EXCLUDED.ip,
				width = EXCLUDED.width,
				height = EXCLUDED.height,
				frigate_name = EXCLUDED.frigate_name,
				status = 'online',
				last_seen = NOW()`,
			cam.ID, orgID, siteID, deviceID,
			cam.Name, cam.Manufacturer, cam.Model, cam.Serial,
			cam.IP, cam.MainStreamURL, cam.SubStreamURL,
			cam.Width, cam.Height, frigateName)
		if err != nil {
			log.Printf("[cameras] upsert %s: %v", cam.ID, err)
		}
	}

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// ─── Camera list / detail ─────────────────────────────────────────────────────

func (a *App) handleListCameras(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	siteFilter := r.URL.Query().Get("site_id")
	limit, offset := parseLimitOffset(r, 100, 500)

	where := `WHERE org_id = $1`
	args := []interface{}{claims.OrgID}
	i := 2

	if siteFilter != "" {
		where += fmt.Sprintf(" AND site_id = $%d", i)
		args = append(args, siteFilter)
		i++
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM cameras " + where
	if err := a.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	query := `SELECT id, name, manufacturer, model, ip, width, height, status, last_seen, site_id
			  FROM cameras ` + where + fmt.Sprintf(" ORDER BY name LIMIT $%d OFFSET $%d", i, i+1)
	args = append(args, limit, offset)

	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer rows.Close()

	type cam struct {
		ID           string     `json:"id"`
		Name         string     `json:"name"`
		Manufacturer string     `json:"manufacturer"`
		Model        string     `json:"model"`
		IP           string     `json:"ip"`
		Width        int        `json:"width"`
		Height       int        `json:"height"`
		Status       string     `json:"status"`
		LastSeen     *time.Time `json:"last_seen"`
		SiteID       *string    `json:"site_id"`
	}

	var cameras []cam
	for rows.Next() {
		var c cam
		var lastSeen sql.NullTime
		var siteID sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.Manufacturer, &c.Model,
			&c.IP, &c.Width, &c.Height, &c.Status, &lastSeen, &siteID); err != nil {
			continue
		}
		if lastSeen.Valid {
			c.LastSeen = &lastSeen.Time
		}
		if siteID.Valid {
			c.SiteID = &siteID.String
		}
		cameras = append(cameras, c)
	}

	writeJSON(w, 200, map[string]interface{}{
		"items":  cameras,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (a *App) handleGetCamera(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	cameraID := chi.URLParam(r, "cameraID")

	var cam struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Manufacturer string `json:"manufacturer"`
		Model        string `json:"model"`
		Serial       string `json:"serial"`
		IP           string `json:"ip"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Status       string `json:"status"`
		PTZSupported bool   `json:"ptz_supported"`
		FrigataName  string `json:"frigate_name"`
	}
	err := a.db.QueryRowContext(r.Context(), `
		SELECT id, name, manufacturer, model, serial, ip, width, height,
		       status, ptz_supported, COALESCE(frigate_name,'')
		FROM cameras WHERE id = $1 AND org_id = $2`,
		cameraID, claims.OrgID).
		Scan(&cam.ID, &cam.Name, &cam.Manufacturer, &cam.Model, &cam.Serial,
			&cam.IP, &cam.Width, &cam.Height, &cam.Status, &cam.PTZSupported, &cam.FrigataName)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, cam)
}

func (a *App) handleCreateCamera(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	var body struct {
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
		PTZSupported  bool   `json:"ptz_supported"`
		SiteID        string `json:"site_id"`
		DeviceID      string `json:"device_id"`
		FrigateName   string `json:"frigate_name"`
		Status        string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", 400)
		return
	}

	id := body.ID
	if id == "" {
		id = uuid.New().String()
	}
	if body.FrigateName == "" {
		body.FrigateName = sanitizeFrigateName(body.Name, id)
	}
	if body.Status == "" {
		body.Status = "offline"
	}

	// Validate site/device belong to org if provided
	if body.SiteID != "" {
		var tmp string
		if err := a.db.QueryRowContext(r.Context(),
			`SELECT id FROM sites WHERE id=$1 AND org_id=$2`, body.SiteID, claims.OrgID).
			Scan(&tmp); err == sql.ErrNoRows {
			http.Error(w, "invalid site_id", 400)
			return
		} else if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
	}
	if body.DeviceID != "" {
		var tmp string
		if err := a.db.QueryRowContext(r.Context(),
			`SELECT id FROM devices WHERE id=$1 AND org_id=$2`, body.DeviceID, claims.OrgID).
			Scan(&tmp); err == sql.ErrNoRows {
			http.Error(w, "invalid device_id", 400)
			return
		} else if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
	}

	var siteID *string
	if body.SiteID != "" {
		siteID = &body.SiteID
	}
	var deviceID *string
	if body.DeviceID != "" {
		deviceID = &body.DeviceID
	}
	var ip *string
	if body.IP != "" {
		ip = &body.IP
	}

	_, err := a.db.ExecContext(r.Context(), `
		INSERT INTO cameras
			(id, org_id, site_id, device_id, name, manufacturer, model, serial,
			 ip, main_stream_url, sub_stream_url, width, height, ptz_supported, status, frigate_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::inet,$10,$11,$12,$13,$14,$15,$16)`,
		id, claims.OrgID, siteID, deviceID, body.Name, body.Manufacturer, body.Model,
		body.Serial, ip, body.MainStreamURL, body.SubStreamURL, body.Width, body.Height,
		body.PTZSupported, body.Status, body.FrigateName)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	_ = auditLog(a.db, claims.OrgID, claims.Subject, "camera.create", "camera", id, map[string]interface{}{"name": body.Name})
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *App) handleUpdateCamera(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	cameraID := chi.URLParam(r, "cameraID")

	var body struct {
		Name   *string `json:"name"`
		SiteID *string `json:"site_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	if body.Name != nil {
		a.db.ExecContext(r.Context(),
			`UPDATE cameras SET name=$1 WHERE id=$2 AND org_id=$3`,
			*body.Name, cameraID, claims.OrgID)
	}
	if body.SiteID != nil {
		a.db.ExecContext(r.Context(),
			`UPDATE cameras SET site_id=$1 WHERE id=$2 AND org_id=$3`,
			*body.SiteID, cameraID, claims.OrgID)
	}

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) handleDeleteCamera(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	cameraID := chi.URLParam(r, "cameraID")
	res, err := a.db.ExecContext(r.Context(),
		`DELETE FROM cameras WHERE id=$1 AND org_id=$2`, cameraID, claims.OrgID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, "not found", 404)
		return
	}
	_ = auditLog(a.db, claims.OrgID, claims.Subject, "camera.delete", "camera", cameraID, nil)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ─── HLS stream proxy ─────────────────────────────────────────────────────────

// handleStreamProxy proxies HLS requests to the on-prem Frigate instance
// via the WireGuard tunnel. The client never gets the Frigate URL directly.
func (a *App) handleStreamProxy(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	cameraID := chi.URLParam(r, "cameraID")

	// Get device WireGuard IP for this camera
	var frigateURL, frigataName string
	err := a.db.QueryRowContext(r.Context(), `
		SELECT d.frigate_url, COALESCE(c.frigate_name, '')
		FROM cameras c
		JOIN devices d ON c.device_id = d.id
		WHERE c.id = $1 AND c.org_id = $2`,
		cameraID, claims.OrgID).Scan(&frigateURL, &frigataName)
	if err == sql.ErrNoRows {
		http.Error(w, "camera not found", 404)
		return
	}
	if frigateURL == "" {
		http.Error(w, "device offline", 503)
		return
	}

	// Build target: http://wireguard-ip:5000/vod/frigate-camera-name/...
	tail := chi.URLParam(r, "*")
	targetURL, err := url.Parse(fmt.Sprintf("%s/vod/%s/%s", frigateURL, frigataName, tail))
	if err != nil {
		http.Error(w, "bad target", 500)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Director = func(req *http.Request) {
		req.URL = targetURL
		req.Host = targetURL.Host
		req.Header.Del("Authorization") // don't forward JWT to Frigate
	}

	// Allow HLS from any origin (dashboard is on a different subdomain)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")

	proxy.ServeHTTP(w, r)
}

func (a *App) handleSnapshotProxy(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	cameraID := chi.URLParam(r, "cameraID")

	var frigateURL, frigataName string
	err := a.db.QueryRowContext(r.Context(), `
		SELECT d.frigate_url, COALESCE(c.frigate_name,'')
		FROM cameras c JOIN devices d ON c.device_id = d.id
		WHERE c.id=$1 AND c.org_id=$2`,
		cameraID, claims.OrgID).Scan(&frigateURL, &frigataName)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}

	snapURL, _ := url.Parse(fmt.Sprintf("%s/api/%s/latest.jpg", frigateURL, frigataName))
	proxy := httputil.NewSingleHostReverseProxy(snapURL)
	proxy.Director = func(req *http.Request) {
		req.URL = snapURL
		req.Host = snapURL.Host
	}
	proxy.ServeHTTP(w, r)
}

// ─── Events ───────────────────────────────────────────────────────────────────

func (a *App) handleListEvents(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	q := r.URL.Query()
	cameraFilter := q.Get("camera_id")
	typeFilter := q.Get("type")
	limit, offset := parseLimitOffset(r, 100, 500)

	where := `WHERE org_id=$1`
	args := []interface{}{claims.OrgID}
	i := 2

	if cameraFilter != "" {
		where += fmt.Sprintf(" AND camera_id=$%d", i)
		args = append(args, cameraFilter)
		i++
	}
	if typeFilter != "" {
		where += fmt.Sprintf(" AND type=$%d", i)
		args = append(args, typeFilter)
		i++
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM events " + where
	if err := a.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	query := `SELECT id, camera_id, type, label, score, snapshot_url, started_at
			  FROM events ` + where + fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d OFFSET $%d", i, i+1)
	args = append(args, limit, offset)

	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer rows.Close()

	type event struct {
		ID          string    `json:"id"`
		CameraID    string    `json:"camera_id"`
		Type        string    `json:"type"`
		Label       string    `json:"label"`
		Score       float64   `json:"score"`
		SnapshotURL string    `json:"snapshot_url"`
		StartedAt   time.Time `json:"started_at"`
	}

	var events []event
	for rows.Next() {
		var e event
		var label, snapshot sql.NullString
		var score sql.NullFloat64
		rows.Scan(&e.ID, &e.CameraID, &e.Type, &label, &score, &snapshot, &e.StartedAt)
		e.Label = label.String
		e.Score = score.Float64
		e.SnapshotURL = snapshot.String
		events = append(events, e)
	}

	writeJSON(w, 200, map[string]interface{}{
		"items":  events,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (a *App) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	eventID := chi.URLParam(r, "eventID")

	var e struct {
		ID          string     `json:"id"`
		CameraID    string     `json:"camera_id"`
		Type        string     `json:"type"`
		Label       string     `json:"label"`
		Score       float64    `json:"score"`
		SnapshotURL string     `json:"snapshot_url"`
		ClipURL     string     `json:"clip_url"`
		StartedAt   time.Time  `json:"started_at"`
		EndedAt     *time.Time `json:"ended_at"`
	}
	var label, snapshot, clip sql.NullString
	var score sql.NullFloat64
	var ended sql.NullTime
	err := a.db.QueryRowContext(r.Context(), `
		SELECT id, camera_id, type, label, score, snapshot_url, clip_url, started_at, ended_at
		FROM events WHERE id=$1 AND org_id=$2`,
		eventID, claims.OrgID).
		Scan(&e.ID, &e.CameraID, &e.Type, &label, &score, &snapshot, &clip, &e.StartedAt, &ended)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	e.Label = label.String
	e.Score = score.Float64
	e.SnapshotURL = snapshot.String
	e.ClipURL = clip.String
	if ended.Valid {
		e.EndedAt = &ended.Time
	}
	writeJSON(w, 200, e)
}

// ─── Alert rules ─────────────────────────────────────────────────────────────

func (a *App) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	rows, err := a.db.QueryContext(r.Context(),
		`SELECT id, name, event_types, enabled FROM alert_rules WHERE org_id=$1 ORDER BY name`, claims.OrgID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer rows.Close()

	type rule struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		EventTypes []string `json:"event_types"`
		Enabled    bool     `json:"enabled"`
	}
	var rules []rule
	for rows.Next() {
		var r rule
		var types pq.StringArray
		if err := rows.Scan(&r.ID, &r.Name, &types, &r.Enabled); err != nil {
			continue
		}
		r.EventTypes = []string(types)
		rules = append(rules, r)
	}
	writeJSON(w, 200, rules)
}

func (a *App) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	var body struct {
		Name       string   `json:"name"`
		EventTypes []string `json:"event_types"`
		MinScore   *float64 `json:"min_score"`
		Enabled    *bool    `json:"enabled"`
		SiteID     *string  `json:"site_id"`
		CameraID   *string  `json:"camera_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", 400)
		return
	}
	if len(body.EventTypes) == 0 {
		body.EventTypes = []string{"person"}
	}
	minScore := 0.7
	if body.MinScore != nil {
		minScore = *body.MinScore
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	id := uuid.New().String()
	_, err := a.db.ExecContext(r.Context(), `
		INSERT INTO alert_rules (id, org_id, site_id, camera_id, name, event_types, min_score, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, claims.OrgID, body.SiteID, body.CameraID, body.Name, pq.Array(body.EventTypes), minScore, enabled)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	_ = auditLog(a.db, claims.OrgID, claims.Subject, "alert_rule.create", "alert_rule", id, map[string]interface{}{"name": body.Name})
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *App) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	ruleID := chi.URLParam(r, "ruleID")
	res, err := a.db.ExecContext(r.Context(),
		`DELETE FROM alert_rules WHERE id=$1 AND org_id=$2`, ruleID, claims.OrgID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, "not found", 404)
		return
	}
	_ = auditLog(a.db, claims.OrgID, claims.Subject, "alert_rule.delete", "alert_rule", ruleID, nil)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ─── Sites ────────────────────────────────────────────────────────────────────

func (a *App) handleListSites(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	rows, err := a.db.QueryContext(r.Context(),
		`SELECT id, name, address, timezone FROM sites WHERE org_id=$1 ORDER BY name`, claims.OrgID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer rows.Close()

	type site struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Address  string `json:"address"`
		Timezone string `json:"timezone"`
	}
	var sites []site
	for rows.Next() {
		var s site
		var addr sql.NullString
		rows.Scan(&s.ID, &s.Name, &addr, &s.Timezone)
		s.Address = addr.String
		sites = append(sites, s)
	}
	writeJSON(w, 200, sites)
}

func (a *App) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	var body struct {
		Name     string `json:"name"`
		Address  string `json:"address"`
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if body.Timezone == "" {
		body.Timezone = "UTC"
	}

	id := uuid.New().String()
	_, err := a.db.ExecContext(r.Context(),
		`INSERT INTO sites (id, org_id, name, address, timezone) VALUES ($1,$2,$3,$4,$5)`,
		id, claims.OrgID, body.Name, body.Address, body.Timezone)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	_ = auditLog(a.db, claims.OrgID, claims.Subject, "site.create", "site", id, map[string]interface{}{"name": body.Name})
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *App) handleGetSite(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	siteID := chi.URLParam(r, "siteID")
	var s struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Address  string `json:"address"`
		Timezone string `json:"timezone"`
	}
	var addr sql.NullString
	err := a.db.QueryRowContext(r.Context(),
		`SELECT id, name, address, timezone FROM sites WHERE id=$1 AND org_id=$2`,
		siteID, claims.OrgID).
		Scan(&s.ID, &s.Name, &addr, &s.Timezone)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	s.Address = addr.String
	writeJSON(w, 200, s)
}

func (a *App) handleGetMyOrg(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	var org struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
		Plan string `json:"plan"`
	}
	a.db.QueryRowContext(r.Context(),
		`SELECT id, name, slug, plan FROM orgs WHERE id=$1`, claims.OrgID).
		Scan(&org.ID, &org.Name, &org.Slug, &org.Plan)
	writeJSON(w, 200, org)
}

func (a *App) handleCreateProvisionToken(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	token := fmt.Sprintf("prov_%s", uuid.New().String())
	_, err := a.db.ExecContext(r.Context(),
		`INSERT INTO provision_tokens (token, org_id, created_by) VALUES ($1,$2,$3)`,
		token, claims.OrgID, claims.Subject)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	_ = auditLog(a.db, claims.OrgID, claims.Subject, "provision_token.create", "provision_token", token, nil)
	writeJSON(w, 201, map[string]string{"token": token})
}

// ─── Provisioning ────────────────────────────────────────────────────────────

func (a *App) handleProvision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" || body.DeviceID == "" {
		http.Error(w, "bad request", 400)
		return
	}

	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer tx.Rollback()

	var orgID string
	var siteID sql.NullString
	var usedAt sql.NullTime
	var expiresAt time.Time
	err = tx.QueryRowContext(r.Context(),
		`SELECT org_id, site_id, used_at, expires_at FROM provision_tokens WHERE token=$1 FOR UPDATE`,
		body.Token).Scan(&orgID, &siteID, &usedAt, &expiresAt)
	if err == sql.ErrNoRows {
		http.Error(w, "invalid token", 403)
		return
	}
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	if usedAt.Valid {
		http.Error(w, "token already used", 403)
		return
	}
	if time.Now().After(expiresAt) {
		http.Error(w, "token expired", 403)
		return
	}

	wgPriv, wgPub, err := generateWireGuardKeypair()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	wgIP, err := allocateWGIP(r.Context(), tx)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	deviceKey := "devkey_" + uuid.New().String()
	var deviceDBID string
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO devices (org_id, site_id, name, device_key, status, wg_ip, wg_public_key, frigate_url)
		VALUES ($1,$2,$3,$4,'pending',$5,$6,$7)
		RETURNING id`,
		orgID, siteID, "Edge NVR "+body.DeviceID, deviceKey, wgIP, wgPub, fmt.Sprintf("http://%s:5000", wgIP)).
		Scan(&deviceDBID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	if err := addWireGuardPeer(a.cfg, wgPub, wgIP); err != nil {
		http.Error(w, "wireguard error", 500)
		return
	}

	_, err = tx.ExecContext(r.Context(),
		`UPDATE provision_tokens SET used_at=NOW(), device_id=$1 WHERE token=$2`,
		deviceDBID, body.Token)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	_ = auditLogTx(tx, orgID, body.DeviceID, "device.provision", "device", deviceDBID, map[string]interface{}{
		"wg_ip": wgIP,
	})

	if err := tx.Commit(); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	writeJSON(w, 200, map[string]string{
		"device_key":      deviceKey,
		"wg_private_key":  wgPriv,
		"wg_ip":           wgIP,
		"server_pubkey":   a.cfg.WGServerPubKey,
		"server_endpoint": fmt.Sprintf("%s:51820", a.cfg.Domain),
	})
}

// ─── MQTT → WebSocket bridge ──────────────────────────────────────────────────

// handleMQTTEvent receives Frigate MQTT events and fans them to WebSocket clients.
// Frigate publishes to: frigate/{camera_name}/events, frigate/{camera_name}/motion, etc.
func (a *App) handleMQTTEvent(client mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	payload := msg.Payload()

	// Parse Frigate event JSON
	var frigateEvent map[string]interface{}
	if err := json.Unmarshal(payload, &frigateEvent); err != nil {
		return
	}

	// Map camera name back to our camera ID via DB
	// Topics:
	//   frigate/{frigate_camera_name}/events
	//   anpr/{frigate_camera_name}
	parts := strings.Split(topic, "/")
	if len(parts) < 2 {
		return
	}
	frigataName := parts[1]
	eventType := ""
	if parts[0] == "frigate" {
		if len(parts) < 3 {
			return
		}
		eventType = parts[2]
	} else if parts[0] == "anpr" {
		eventType = "anpr"
	} else {
		return
	}

	// Store event in DB (best-effort, don't block)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var cameraID, orgID string
		var siteID sql.NullString
		err := a.db.QueryRowContext(ctx,
			`SELECT id, org_id, site_id FROM cameras WHERE frigate_name=$1`, frigataName).
			Scan(&cameraID, &orgID, &siteID)
		if err != nil {
			return
		}

		eventID := uuid.New().String()
		payloadJSON, _ := json.Marshal(frigateEvent)

		label, score, frigateEventID := extractFrigateDetails(frigateEvent)
		if eventType == "anpr" {
			if v, ok := frigateEvent["plate_text"].(string); ok && v != "" {
				label = v
			}
			if v, ok := frigateEvent["confidence"].(float64); ok {
				score = v
			}
		}
		a.db.ExecContext(ctx, `
			INSERT INTO events (id, org_id, site_id, camera_id, type, label, score, payload, started_at, frigate_event_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW(),$9)`,
			eventID, orgID, siteID, cameraID, eventType, label, score, payloadJSON, frigateEventID)

		// Push to WebSocket clients subscribed to this org
		wsMsg, _ := json.Marshal(map[string]interface{}{
			"type":       "event",
			"event_id":   eventID,
			"camera_id":  cameraID,
			"event_type": eventType,
			"payload":    frigateEvent,
			"timestamp":  time.Now().UTC(),
		})
		a.hub.BroadcastToOrg(orgID, wsMsg)

		// Evaluate alert rules (best-effort)
		a.evaluateAlertRules(ctx, orgID, siteID.String, cameraID, eventType, score, eventID)
	}()
}

func extractFrigateDetails(evt map[string]interface{}) (label string, score float64, eventID string) {
	after, ok := evt["after"].(map[string]interface{})
	if !ok {
		return "", 0, ""
	}
	if v, ok := after["label"].(string); ok {
		label = v
	}
	switch v := after["score"].(type) {
	case float64:
		score = v
	case int:
		score = float64(v)
	}
	if v, ok := after["id"].(string); ok {
		eventID = v
	}
	return
}

func (a *App) evaluateAlertRules(ctx context.Context, orgID, siteID, cameraID, eventType string, score float64, eventID string) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, event_types, min_score, active_from, active_to, active_days, cooldown_secs, last_triggered
		FROM alert_rules
		WHERE org_id=$1 AND enabled=true
		  AND (camera_id IS NULL OR camera_id=$2)
		  AND (site_id IS NULL OR site_id=$3)`,
		orgID, cameraID, siteID)
	if err != nil {
		return
	}
	defer rows.Close()

	now := time.Now().UTC()
	for rows.Next() {
		var (
			ruleID        string
			eventTypes    pq.StringArray
			minScore      float64
			activeFrom    sql.NullString
			activeTo      sql.NullString
			activeDays    pq.Int64Array
			cooldownSecs  int
			lastTriggered sql.NullTime
		)
		if err := rows.Scan(&ruleID, &eventTypes, &minScore, &activeFrom, &activeTo, &activeDays, &cooldownSecs, &lastTriggered); err != nil {
			continue
		}
		if !eventTypeAllowed(eventType, []string(eventTypes)) {
			continue
		}
		if score > 0 && score < minScore {
			continue
		}
		if !withinSchedule(now, activeFrom, activeTo, activeDays) {
			continue
		}
		if lastTriggered.Valid && now.Sub(lastTriggered.Time) < time.Duration(cooldownSecs)*time.Second {
			continue
		}

		_, _ = a.db.ExecContext(ctx,
			`UPDATE alert_rules SET last_triggered=$1 WHERE id=$2`, now, ruleID)
		_ = auditLog(a.db, orgID, "system", "alert_rule.trigger", "alert_rule", ruleID, map[string]interface{}{
			"event_id":  eventID,
			"camera_id": cameraID,
			"type":      eventType,
		})
		wsMsg, _ := json.Marshal(map[string]interface{}{
			"type":       "alert",
			"rule_id":    ruleID,
			"event_id":   eventID,
			"camera_id":  cameraID,
			"event_type": eventType,
			"timestamp":  now,
		})
		a.hub.BroadcastToOrg(orgID, wsMsg)
	}
}

func eventTypeAllowed(eventType string, allowed []string) bool {
	for _, t := range allowed {
		if t == eventType {
			return true
		}
	}
	return false
}

func withinSchedule(now time.Time, from sql.NullString, to sql.NullString, days pq.Int64Array) bool {
	if len(days) > 0 {
		wd := int64(now.Weekday())
		ok := false
		for _, d := range days {
			if d == wd {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if !from.Valid && !to.Valid {
		return true
	}

	nowSecs := now.UTC().Hour()*3600 + now.UTC().Minute()*60 + now.UTC().Second()
	fromSecs, hasFrom := parseTimeOfDay(from)
	toSecs, hasTo := parseTimeOfDay(to)

	if hasFrom && hasTo {
		if fromSecs <= toSecs {
			return nowSecs >= fromSecs && nowSecs <= toSecs
		}
		// overnight window
		return nowSecs >= fromSecs || nowSecs <= toSecs
	}
	if hasFrom {
		return nowSecs >= fromSecs
	}
	if hasTo {
		return nowSecs <= toSecs
	}
	return true
}

func parseTimeOfDay(v sql.NullString) (int, bool) {
	if !v.Valid {
		return 0, false
	}
	s := v.String
	t, err := time.Parse("15:04:05", s)
	if err != nil {
		t, err = time.Parse("15:04", s)
		if err != nil {
			return 0, false
		}
	}
	return t.Hour()*3600 + t.Minute()*60 + t.Second(), true
}

// ─── WebSocket hub ────────────────────────────────────────────────────────────

type WSClient struct {
	conn  *websocket.Conn
	orgID string
	send  chan []byte
}

type WSHub struct {
	clients    map[*WSClient]bool
	mu         sync.RWMutex
	register   chan *WSClient
	unregister chan *WSClient
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients:    make(map[*WSClient]bool),
		register:   make(chan *WSClient, 16),
		unregister: make(chan *WSClient, 16),
	}
}

func (h *WSHub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
			close(c.send)
		}
	}
}

func (h *WSHub) BroadcastToOrg(orgID string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.orgID == orgID {
			select {
			case c.send <- msg:
			default: // client too slow — drop
			}
		}
	}
}

func (a *App) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &WSClient{conn: conn, orgID: claims.OrgID, send: make(chan []byte, 64)}
	a.hub.register <- client

	// Write pump
	go func() {
		defer func() {
			a.hub.unregister <- client
			conn.Close()
		}()
		for msg := range client.send {
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// Read pump (ping/pong keepalive)
	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// ─── JWT middleware ───────────────────────────────────────────────────────────

type Claims struct {
	Subject string
	OrgID   string
	Email   string
	Roles   []string
}

func claimsFromCtx(ctx context.Context) Claims {
	if c := auth.ClaimsFromContext(ctx); c != nil {
		return Claims{
			Subject: c.Subject,
			OrgID:   c.OrgID,
			Email:   c.Email,
			Roles:   c.Roles,
		}
	}
	return Claims{}
}

// sanitizeFrigateName matches the agent's camera key format.
func sanitizeFrigateName(name, id string) string {
	shortID := strings.ReplaceAll(id, "-", "")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return '_'
	}, name)
	for strings.Contains(safe, "__") {
		safe = strings.ReplaceAll(safe, "__", "_")
	}
	safe = strings.Trim(safe, "_")
	return fmt.Sprintf("%s_%s", safe, shortID)
}

// generateWireGuardKeypair creates a WireGuard X25519 keypair.
func generateWireGuardKeypair() (privateKey string, publicKey string, err error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", "", err
	}
	// Clamp per RFC 7748.
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}

	return base64.StdEncoding.EncodeToString(priv[:]),
		base64.StdEncoding.EncodeToString(pub), nil
}

// allocateWGIP assigns the next available 10.10.0.x address.
func allocateWGIP(ctx context.Context, tx *sql.Tx) (string, error) {
	var maxOctet sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX((split_part(wg_ip::text, '.', 4))::int) FROM devices WHERE wg_ip IS NOT NULL`).
		Scan(&maxOctet); err != nil {
		return "", err
	}
	start := int64(10)
	if maxOctet.Valid && maxOctet.Int64 >= start {
		start = maxOctet.Int64 + 1
	}
	if start >= 255 {
		return "", fmt.Errorf("wireguard address pool exhausted")
	}
	return fmt.Sprintf("10.10.0.%d", start), nil
}

// addWireGuardPeer inserts a new peer into the WireGuard server container.
func addWireGuardPeer(cfg Config, publicKey, wgIP string) error {
	if publicKey == "" || wgIP == "" {
		return fmt.Errorf("missing peer data")
	}
	cmd := exec.Command(
		"docker", "exec", cfg.WGContainer, "wg", "set", cfg.WGInterface,
		"peer", publicKey, "allowed-ips", wgIP+"/32",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	// Persist peer to config so it survives container restarts.
	save := exec.Command(
		"docker", "exec", cfg.WGContainer, "wg-quick", "save", cfg.WGInterface,
	)
	if out, err = save.CombinedOutput(); err != nil {
		return fmt.Errorf("wg-quick save failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// pqArray is a minimal helper to pass string slices to pq without importing it globally.
func auditLog(db *sql.DB, orgID, actor, action, resourceType, resourceID string, payload interface{}) error {
	data, _ := json.Marshal(payload)
	_, err := db.Exec(
		`INSERT INTO audit_log (org_id, actor, action, resource_type, resource_id, payload)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, actor, action, resourceType, resourceID, data)
	return err
}

func auditLogTx(tx *sql.Tx, orgID, actor, action, resourceType, resourceID string, payload interface{}) error {
	data, _ := json.Marshal(payload)
	_, err := tx.Exec(
		`INSERT INTO audit_log (org_id, actor, action, resource_type, resource_id, payload)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, actor, action, resourceType, resourceID, data)
	return err
}

// ─── MQTT ─────────────────────────────────────────────────────────────────────

func connectMQTT(cfg Config) mqtt.Client {
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID("cam-api-" + uuid.New().String()).
		SetUsername(cfg.MQTTUser).
		SetPassword(cfg.MQTTPassword).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	client := mqtt.NewClient(opts)
	if tok := client.Connect(); tok.Wait() && tok.Error() != nil {
		log.Fatalf("mqtt connect: %v", tok.Error())
	}
	return client
}

// ─── CORS ─────────────────────────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Device-Key")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

var (
	reqCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cam_api_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"method", "path", "status"},
	)
	reqDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cam_api_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(sr, r)
		path := r.URL.Path
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			path = rc.RoutePattern()
		}
		status := fmt.Sprintf("%d", sr.status)
		reqCount.WithLabelValues(r.Method, path, status).Inc()
		reqDuration.WithLabelValues(r.Method, path, status).Observe(time.Since(start).Seconds())
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func parseLimitOffset(r *http.Request, def, max int) (int, int) {
	limit := def
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				n = 1
			}
			if n > max {
				n = max
			}
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("required env %s not set", k)
	}
	return v
}

func getEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
