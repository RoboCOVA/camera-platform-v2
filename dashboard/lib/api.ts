// lib/api.ts — typed API client for the cam-platform backend
import { getSession } from 'next-auth/react'

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? ''

// ─── Types ────────────────────────────────────────────────────────────────────

export interface Camera {
  id: string
  name: string
  manufacturer: string
  model: string
  ip: string
  width: number
  height: number
  status: 'online' | 'offline' | 'error'
  last_seen: string | null
  site_id: string | null
}

export interface Site {
  id: string
  name: string
  address: string
  timezone: string
}

export interface CamEvent {
  id: string
  camera_id: string
  type: string
  label: string
  score: number
  snapshot_url: string
  started_at: string
}

export interface AlertRule {
  id: string
  name: string
  event_types: string[]
  enabled: boolean
}

export interface Org {
  id: string
  name: string
  slug: string
  plan: string
}

// ─── Core fetch ───────────────────────────────────────────────────────────────

async function apiFetch<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const session = await getSession()
  if (!session?.accessToken) throw new Error('Not authenticated')
  if (session.error === 'RefreshAccessTokenError') {
    // Force re-login
    window.location.href = '/api/auth/signin'
    throw new Error('Session expired')
  }

  const url = `${API_URL}${path}`
  const resp = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${session.accessToken}`,
      ...options.headers,
    },
  })

  if (resp.status === 401) {
    window.location.href = '/api/auth/signin'
    throw new Error('Unauthorized')
  }
  if (!resp.ok) {
    const body = await resp.text()
    throw new Error(`API error ${resp.status}: ${body}`)
  }

  const text = await resp.text()
  return text ? JSON.parse(text) : null
}

// ─── Cameras ─────────────────────────────────────────────────────────────────

export const cameras = {
  list: (siteId?: string) => {
    const qs = siteId ? `?site_id=${siteId}` : ''
    return apiFetch<Camera[]>(`/api/cameras${qs}`)
  },
  get: (id: string) => apiFetch<Camera>(`/api/cameras/${id}`),
  update: (id: string, body: Partial<Pick<Camera, 'name' | 'site_id'>>) =>
    apiFetch<void>(`/api/cameras/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  streamUrl: (id: string) => `${API_URL}/api/stream/${id}/hls/live.m3u8`,
  snapshotUrl: (id: string) => `${API_URL}/api/cameras/${id}/snapshot`,
}

// ─── Sites ────────────────────────────────────────────────────────────────────

export const sites = {
  list: () => apiFetch<Site[]>('/api/sites'),
  get: (id: string) => apiFetch<Site>(`/api/sites/${id}`),
  create: (body: Pick<Site, 'name' | 'address' | 'timezone'>) =>
    apiFetch<{ id: string }>('/api/sites', { method: 'POST', body: JSON.stringify(body) }),
}

// ─── Events ───────────────────────────────────────────────────────────────────

export const events = {
  list: (params?: { camera_id?: string; type?: string }) => {
    const qs = params ? '?' + new URLSearchParams(params as any).toString() : ''
    return apiFetch<CamEvent[]>(`/api/events${qs}`)
  },
}

// ─── Org ─────────────────────────────────────────────────────────────────────

export const org = {
  me: () => apiFetch<Org>('/api/orgs/me'),
}

// ─── Alert rules ─────────────────────────────────────────────────────────────

export const alertRules = {
  list: () => apiFetch<AlertRule[]>('/api/alert-rules'),
  create: (body: Pick<AlertRule, 'name' | 'event_types'>) =>
    apiFetch<{ id: string }>('/api/alert-rules', { method: 'POST', body: JSON.stringify(body) }),
  delete: (id: string) => apiFetch<void>(`/api/alert-rules/${id}`, { method: 'DELETE' }),
}

// ─── WebSocket ────────────────────────────────────────────────────────────────

export type WSEvent = {
  type: 'event'
  event_id: string
  camera_id: string
  event_type: string
  payload: Record<string, any>
  timestamp: string
}

export function connectEventStream(
  token: string,
  onEvent: (e: WSEvent) => void,
  onStatusChange?: (connected: boolean) => void
): () => void {
  const wsBase = API_URL.replace(/^http/, 'ws')
  let ws: WebSocket | null = null
  let closed = false
  let retryDelay = 1000

  function connect() {
    if (closed) return
    ws = new WebSocket(`${wsBase}/ws/events?token=${token}`)

    ws.onopen = () => {
      retryDelay = 1000
      onStatusChange?.(true)
    }

    ws.onmessage = (msg) => {
      try {
        const data = JSON.parse(msg.data) as WSEvent
        onEvent(data)
      } catch {}
    }

    ws.onclose = () => {
      onStatusChange?.(false)
      if (!closed) {
        setTimeout(connect, retryDelay)
        retryDelay = Math.min(retryDelay * 2, 30_000)
      }
    }

    ws.onerror = () => ws?.close()
  }

  connect()

  return () => {
    closed = true
    ws?.close()
  }
}
