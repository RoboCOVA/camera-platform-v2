-- =============================================================================
-- cam-platform database schema
-- Multi-tenant: every table scoped to org_id
-- =============================================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─── Organizations ────────────────────────────────────────────────────────────

CREATE TABLE orgs (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name          TEXT NOT NULL,
  slug          TEXT UNIQUE NOT NULL,        -- used in Keycloak realm name
  plan          TEXT NOT NULL DEFAULT 'starter', -- starter | pro | enterprise
  timezone      TEXT NOT NULL DEFAULT 'UTC',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Sites (physical locations within an org) ─────────────────────────────────

CREATE TABLE sites (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  address       TEXT,
  timezone      TEXT NOT NULL DEFAULT 'UTC',
  -- WireGuard peer info (set when agent provisions)
  wg_public_key TEXT,
  wg_ip         INET,      -- e.g. 10.10.0.5
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX sites_org_id ON sites(org_id);

-- ─── Edge devices (NVR boxes running the agent) ───────────────────────────────

CREATE TABLE devices (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  site_id         UUID REFERENCES sites(id) ON DELETE SET NULL,
  name            TEXT NOT NULL DEFAULT 'Edge NVR',
  -- Identity
  device_key      TEXT UNIQUE NOT NULL,   -- pre-shared key for heartbeat auth
  agent_version   TEXT,
  -- Status
  status          TEXT NOT NULL DEFAULT 'pending',  -- pending | online | offline
  last_seen       TIMESTAMPTZ,
  -- Network (WireGuard)
  wg_ip           INET,          -- assigned WireGuard IP for this device
  -- Frigate
  frigate_url     TEXT,          -- http://wg-ip:5000 — only reachable via WireGuard
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX devices_org_id ON devices(org_id);
CREATE INDEX devices_site_id ON devices(site_id);
CREATE INDEX devices_status ON devices(status);
CREATE INDEX devices_last_seen ON devices(last_seen);

-- ─── Cameras (discovered ONVIF cameras) ──────────────────────────────────────

CREATE TABLE cameras (
  id              UUID PRIMARY KEY,         -- stable ID from discovery (SHA-1 of serial)
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  site_id         UUID REFERENCES sites(id) ON DELETE SET NULL,
  device_id       UUID REFERENCES devices(id) ON DELETE SET NULL,
  -- Identity
  name            TEXT NOT NULL,
  manufacturer    TEXT,
  model           TEXT,
  serial          TEXT,
  firmware_ver    TEXT,
  ip              INET,
  onvif_port      INT DEFAULT 80,
  -- Streams (stored encrypted — contains RTSP credentials)
  main_stream_url TEXT,
  sub_stream_url  TEXT,
  -- Capabilities
  width           INT,
  height          INT,
  ptz_supported   BOOL NOT NULL DEFAULT FALSE,
  -- Status
  status          TEXT NOT NULL DEFAULT 'online',  -- online | offline | error
  last_seen       TIMESTAMPTZ,
  -- Frigate camera name key (e.g. "hikvision_ds2cd_a1b2c3d4")
  frigate_name    TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX cameras_org_id ON cameras(org_id);
CREATE INDEX cameras_site_id ON cameras(site_id);
CREATE INDEX cameras_device_id ON cameras(device_id);
CREATE INDEX cameras_status ON cameras(status);

-- ─── Events (motion, object detection, alarms) ───────────────────────────────

CREATE TABLE events (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  site_id         UUID REFERENCES sites(id),
  camera_id       UUID REFERENCES cameras(id) ON DELETE SET NULL,
  -- Event data
  type            TEXT NOT NULL,     -- motion | person | car | bicycle | alarm | offline
  label           TEXT,              -- object detection label (person, car, etc.)
  score           FLOAT,             -- detection confidence 0.0–1.0
  -- Media
  snapshot_url    TEXT,              -- thumbnail stored in S3 or locally
  clip_url        TEXT,              -- short video clip if recorded
  -- Frigate native event ID (for linking back to recordings)
  frigate_event_id TEXT,
  -- Payload for extensibility
  payload         JSONB,
  -- Time
  started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at        TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partition by org + time for scale (run queries per-org, recent events first)
CREATE INDEX events_org_created ON events(org_id, created_at DESC);
CREATE INDEX events_camera_created ON events(camera_id, created_at DESC);
CREATE INDEX events_type ON events(type);
CREATE INDEX events_site_created ON events(site_id, created_at DESC);

-- ─── Notifications (alert rules) ─────────────────────────────────────────────

CREATE TABLE alert_rules (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  site_id         UUID REFERENCES sites(id),
  camera_id       UUID REFERENCES cameras(id),
  name            TEXT NOT NULL,
  -- Trigger
  event_types     TEXT[] NOT NULL DEFAULT '{person}',  -- which event types trigger
  min_score       FLOAT NOT NULL DEFAULT 0.7,
  -- Schedule (null = always active)
  active_from     TIME,     -- e.g. 22:00
  active_to       TIME,     -- e.g. 06:00
  active_days     INT[],    -- 0=Sun..6=Sat; null = all days
  -- Delivery
  notify_email    TEXT[],
  notify_webhook  TEXT,
  notify_push     BOOL DEFAULT TRUE,
  -- Cooldown: don't re-alert within N seconds
  cooldown_secs   INT NOT NULL DEFAULT 300,
  last_triggered  TIMESTAMPTZ,
  enabled         BOOL NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX alert_rules_org_id ON alert_rules(org_id);

-- ─── Users ────────────────────────────────────────────────────────────────────
-- Users are managed in Keycloak. This table mirrors org membership + roles.

CREATE TABLE org_members (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  keycloak_user_id TEXT NOT NULL,    -- sub claim from JWT
  email           TEXT NOT NULL,
  role            TEXT NOT NULL DEFAULT 'viewer',  -- owner | admin | manager | viewer
  site_ids        UUID[],            -- null = access all sites; non-null = limited
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(org_id, keycloak_user_id)
);

CREATE INDEX org_members_org_id ON org_members(org_id);
CREATE INDEX org_members_keycloak_id ON org_members(keycloak_user_id);

-- ─── Device provisioning tokens ──────────────────────────────────────────────

CREATE TABLE provision_tokens (
  token           TEXT PRIMARY KEY,
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  site_id         UUID REFERENCES sites(id),
  created_by      TEXT NOT NULL,      -- keycloak_user_id
  used_at         TIMESTAMPTZ,
  device_id       UUID REFERENCES devices(id),
  expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Audit log ───────────────────────────────────────────────────────────────

CREATE TABLE audit_log (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  actor           TEXT NOT NULL,      -- keycloak_user_id or "system" or device_id
  action          TEXT NOT NULL,      -- camera.delete, user.invite, site.create, etc.
  resource_type   TEXT,
  resource_id     TEXT,
  payload         JSONB,
  ip_address      INET,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX audit_log_org_created ON audit_log(org_id, created_at DESC);

-- ─── Updated_at trigger ───────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER orgs_updated_at    BEFORE UPDATE ON orgs    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER sites_updated_at   BEFORE UPDATE ON sites   FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER devices_updated_at BEFORE UPDATE ON devices FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER cameras_updated_at BEFORE UPDATE ON cameras FOR EACH ROW EXECUTE FUNCTION set_updated_at();
