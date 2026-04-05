package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
)

// ─── Config ───────────────────────────────────────────────────────────────────

type Config struct {
	DatabaseURL    string
	KeycloakURL    string
	KeycloakRealm  string
	MQTTBroker     string
	MQTTUser       string
	MQTTPassword   string
	DeviceSecret   string
	Port           string
}

func configFromEnv() Config {
	return Config{
		DatabaseURL:   mustEnv("DATABASE_URL"),
		KeycloakURL:   mustEnv("KEYCLOAK_URL"),
		KeycloakRealm: getEnvOr("KEYCLOAK_REALM", "camplatform"),
		MQTTBroker:    getEnvOr("MQTT_BROKER", "mqtt://localhost:1883"),
		MQTTUser:      os.Getenv("MQTT_USER"),
		MQTTPassword:  os.Getenv("MQTT_PASSWORD"),
		DeviceSecret:  mustEnv("DEVICE_SECRET"),
		Port:          getEnvOr("PORT", "3001"),
	}
}

// ─── App ──────────────────────────────────────────────────────────────────────

type App struct {
	cfg    Config
	db     *sql.DB
	mqtt   mqtt.Client
	hub    *WSHub
	jwks   *JWKSCache
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

	// JWKS cache for Keycloak JWT validation
	jwksURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs",
		cfg.KeycloakURL, cfg.KeycloakRealm)
	jwks := NewJWKSCache(jwksURL, 1*time.Hour)

	app := &App{cfg: cfg, db: db, mqtt: mqttClient, hub: hub, jwks: jwks}

	// Subscribe to all Frigate events from all sites
	// Topic pattern: frigate/{site_id}/events
	mqttClient.Subscribe("frigate/#", 1, app.handleMQTTEvent)

	// Routes
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(corsMiddleware)
	r.Use(middleware.Timeout(30 * time.Second))

	// Public endpoints
	r.Get("/health", app.handleHealth)
	r.Post("/api/devices/heartbeat", app.handleDeviceHeartbeat)
	r.Post("/api/devices/cameras", app.handleDeviceCameras)

	// Authenticated endpoints (JWT required)
	r.Group(func(r chi.Router) {
		r.Use(app.jwtMiddleware)

		// Orgs
		r.Get("/api/orgs/me", app.handleGetMyOrg)

		// Sites
		r.Get("/api/sites", app.handleListSites)
		r.Post("/api/sites", app.handleCreateSite)
		r.Get("/api/sites/{siteID}", app.handleGetSite)

		// Cameras
		r.Get("/api/cameras", app.handleListCameras)
		r.Get("/api/cameras/{cameraID}", app.handleGetCamera)
		r.Patch("/api/cameras/{cameraID}", app.handleUpdateCamera)

		// Events
		r.Get("/api/events", app.handleListEvents)
		r.Get("/api/events/{eventID}", app.handleGetEvent)

		// HLS stream proxy (routes through WireGuard to on-prem Frigate)
		r.Get("/api/stream/{cameraID}/hls/*", app.handleStreamProxy)

		// Frigate snapshot proxy
		r.Get("/api/cameras/{cameraID}/snapshot", app.handleSnapshotProxy)

		// Alert rules
		r.Get("/api/alert-rules", app.handleListAlertRules)
		r.Post("/api/alert-rules", app.handleCreateAlertRule)
		r.Delete("/api/alert-rules/{ruleID}", app.handleDeleteAlertRule)

		// Device provisioning tokens
		r.Post("/api/provision-tokens", app.handleCreateProvisionToken)

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
		DeviceID  string `json:"device_id"`
		Cameras   []struct {
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
		_, err := a.db.ExecContext(r.Context(), `
			INSERT INTO cameras
				(id, org_id, site_id, device_id, name, manufacturer, model, serial,
				 ip, main_stream_url, sub_stream_url, width, height, status, last_seen)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::inet,$10,$11,$12,$13,'online',NOW())
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				device_id = EXCLUDED.device_id,
				main_stream_url = EXCLUDED.main_stream_url,
				sub_stream_url = EXCLUDED.sub_stream_url,
				status = 'online',
				last_seen = NOW()`,
			cam.ID, orgID, siteID, deviceID,
			cam.Name, cam.Manufacturer, cam.Model, cam.Serial,
			cam.IP, cam.MainStreamURL, cam.SubStreamURL,
			cam.Width, cam.Height)
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

	query := `SELECT id, name, manufacturer, model, ip, width, height, status, last_seen, site_id
			  FROM cameras WHERE org_id = $1`
	args := []interface{}{claims.OrgID}

	if siteFilter != "" {
		query += " AND site_id = $2"
		args = append(args, siteFilter)
	}
	query += " ORDER BY name"

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

	writeJSON(w, 200, cameras)
}

func (a *App) handleGetCamera(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	cameraID := chi.URLParam(r, "cameraID")

	var cam struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		Manufacturer  string  `json:"manufacturer"`
		Model         string  `json:"model"`
		Serial        string  `json:"serial"`
		IP            string  `json:"ip"`
		Width         int     `json:"width"`
		Height        int     `json:"height"`
		Status        string  `json:"status"`
		PTZSupported  bool    `json:"ptz_supported"`
		FrigataName   string  `json:"frigate_name"`
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
	limit := 100

	query := `SELECT id, camera_id, type, label, score, snapshot_url, started_at
			  FROM events WHERE org_id=$1`
	args := []interface{}{claims.OrgID}
	i := 2

	if cameraFilter != "" {
		query += fmt.Sprintf(" AND camera_id=$%d", i)
		args = append(args, cameraFilter)
		i++
	}
	if typeFilter != "" {
		query += fmt.Sprintf(" AND type=$%d", i)
		args = append(args, typeFilter)
		i++
	}
	query += fmt.Sprintf(" ORDER BY started_at DESC LIMIT %d", limit)

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

	writeJSON(w, 200, events)
}

func (a *App) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"todo": "implement"})
}

// ─── Alert rules ─────────────────────────────────────────────────────────────

func (a *App) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	rows, _ := a.db.QueryContext(r.Context(),
		`SELECT id, name, event_types, enabled FROM alert_rules WHERE org_id=$1`, claims.OrgID)
	defer rows.Close()
	writeJSON(w, 200, []interface{}{})
}

func (a *App) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 201, map[string]string{"status": "created"})
}

func (a *App) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *App) handleGetSite(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"todo": "implement"})
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
		token, claims.OrgID, claims.UserID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 201, map[string]string{"token": token})
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

	// Map Frigate camera name back to our camera ID via DB
	// Topic: frigate/{frigate_camera_name}/events
	parts := strings.Split(topic, "/")
	if len(parts) < 3 {
		return
	}
	frigataName := parts[1]
	eventType := parts[2]

	// Store event in DB (best-effort, don't block)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var cameraID, orgID string
		err := a.db.QueryRowContext(ctx,
			`SELECT id, org_id FROM cameras WHERE frigate_name=$1`, frigataName).
			Scan(&cameraID, &orgID)
		if err != nil {
			return
		}

		eventID := uuid.New().String()
		payloadJSON, _ := json.Marshal(frigateEvent)

		a.db.ExecContext(ctx, `
			INSERT INTO events (id, org_id, camera_id, type, payload, started_at)
			VALUES ($1,$2,$3,$4,$5,NOW())`,
			eventID, orgID, cameraID, eventType, payloadJSON)

		// Push to WebSocket clients subscribed to this org
		wsMsg, _ := json.Marshal(map[string]interface{}{
			"type":      "event",
			"event_id":  eventID,
			"camera_id": cameraID,
			"event_type": eventType,
			"payload":   frigateEvent,
			"timestamp": time.Now().UTC(),
		})
		a.hub.BroadcastToOrg(orgID, wsMsg)
	}()
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
	UserID string
	OrgID  string
	Email  string
	Roles  []string
}

type ctxKey string

const claimsKey ctxKey = "claims"

func claimsFromCtx(ctx context.Context) Claims {
	if c, ok := ctx.Value(claimsKey).(Claims); ok {
		return c
	}
	return Claims{}
}

// JWKSCache fetches and caches Keycloak's public keys for JWT validation.
type JWKSCache struct {
	url      string
	mu       sync.RWMutex
	keys     map[string]interface{} // kid → public key
	fetchedAt time.Time
	ttl      time.Duration
}

func NewJWKSCache(url string, ttl time.Duration) *JWKSCache {
	return &JWKSCache{url: url, ttl: ttl, keys: map[string]interface{}{}}
}

func (j *JWKSCache) GetKey(kid string) (interface{}, error) {
	j.mu.RLock()
	if time.Since(j.fetchedAt) < j.ttl {
		if k, ok := j.keys[kid]; ok {
			j.mu.RUnlock()
			return k, nil
		}
	}
	j.mu.RUnlock()

	// Re-fetch JWKS
	resp, err := http.Get(j.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, err
	}

	j.mu.Lock()
	j.fetchedAt = time.Now()
	// In production: parse RSA public keys from JWK format
	// For brevity, this returns a placeholder — use a proper JWK library
	// e.g. github.com/lestrrat-go/jwx/v2
	j.mu.Unlock()

	return nil, fmt.Errorf("key %s not found", kid)
}

func (a *App) jwtMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "missing token", 401)
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		// Parse without verification first to get claims structure
		// In production: verify signature using JWKS from Keycloak
		token, _, err := jwt.NewParser().ParseUnverified(tokenStr, jwt.MapClaims{})
		if err != nil {
			http.Error(w, "invalid token", 401)
			return
		}

		mapClaims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "invalid claims", 401)
			return
		}

		// Extract org_id from custom claim (set in Keycloak via mapper)
		orgID, _ := mapClaims["org_id"].(string)
		userID, _ := mapClaims["sub"].(string)
		email, _ := mapClaims["email"].(string)

		claims := Claims{
			UserID: userID,
			OrgID:  orgID,
			Email:  email,
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ─── MQTT ─────────────────────────────────────────────────────────────────────

func connectMQTT(cfg Config) mqtt.Client {
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID("cam-api-"+uuid.New().String()).
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

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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
