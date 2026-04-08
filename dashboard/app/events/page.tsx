'use client'
import { useState, useEffect } from 'react'
import { useSession } from 'next-auth/react'
import useSWR from 'swr'
import { events as eventsAPI, connectEventStream, cameras as cameraAPI } from '@/lib/api'
import type { CamEvent, WSEvent, Camera } from '@/lib/api'
import { formatDistanceToNow, format } from 'date-fns'

const EVENT_TYPES = ['all', 'person', 'car', 'bicycle', 'motion']
const PAGE_SIZE = 50

export default function EventsPage() {
  const { data: session } = useSession()
  const [typeFilter, setTypeFilter] = useState('all')
  const [liveEvents, setLiveEvents] = useState<WSEvent[]>([])
  const [wsConnected, setWsConnected] = useState(false)
  const [page, setPage] = useState(0)

  const { data: eventPage } = useSWR(
    ['events', typeFilter, page],
    () => eventsAPI.list({
      type: typeFilter !== 'all' ? typeFilter : undefined,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }),
    { refreshInterval: page === 0 ? 30_000 : 0 }
  )

  const { data: cameraPage } = useSWR('cameras-for-events', () =>
    cameraAPI.list(undefined, { limit: 500, offset: 0 })
  )
  const cameraMap = Object.fromEntries((cameraPage?.items ?? []).map(c => [c.id, c]))

  // Real-time event stream
  useEffect(() => {
    if (!session?.accessToken) return

    const disconnect = connectEventStream(
      session.accessToken,
      (event) => {
        if (typeFilter === 'all' || event.event_type === typeFilter) {
          setLiveEvents(prev => [event, ...prev].slice(0, 50))
        }
      },
      setWsConnected
    )
    return disconnect
  }, [session?.accessToken, typeFilter])

  useEffect(() => {
    setPage(0)
  }, [typeFilter])

  const historicalEvents = eventPage?.items ?? []
  const total = eventPage?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const pageNumbers = buildPageNumbers(page, totalPages)


  useEffect(() => {
    if (page > totalPages - 1) setPage(0)
  }, [page, totalPages])

  return (
    <div style={{ padding: '24px 28px' }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600, color: '#f1f5f9' }}>Events</h1>
          {wsConnected && liveEvents.length > 0 && (
            <span style={{
              padding: '2px 8px', borderRadius: 10,
              background: '#14532d', color: '#4ade80',
              fontSize: 11, fontWeight: 500,
            }}>
              +{liveEvents.length} new
            </span>
          )}
        </div>
        <div style={{ marginTop: 4, fontSize: 13, color: '#64748b' }}>
          {wsConnected
            ? '● Receiving live events'
            : '○ Connecting to event stream...'}
          {total > 0 && (
            <span style={{ marginLeft: 10, color: '#475569' }}>
              · {Math.min((page + 1) * PAGE_SIZE, total)} of {total}
            </span>
          )}
        </div>
      </div>

      {/* Type filter */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 20 }}>
        {EVENT_TYPES.map(t => (
          <button
            key={t}
            onClick={() => { setTypeFilter(t); setLiveEvents([]) }}
            style={{
              padding: '5px 14px', borderRadius: 20, fontSize: 12,
              cursor: 'pointer', textTransform: 'capitalize',
              background: typeFilter === t ? '#1e3a5f' : '#1a1f2e',
              border: `1px solid ${typeFilter === t ? '#2563eb' : '#1e2535'}`,
              color: typeFilter === t ? '#60a5fa' : '#94a3b8',
              fontWeight: typeFilter === t ? 500 : 400,
            }}
          >
            {t}
          </button>
        ))}
      </div>

      {/* Events */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {/* Live events (real-time, prepended) */}
        {liveEvents.map(ev => (
          <EventCard
            key={ev.event_id}
            id={ev.event_id}
            type={ev.event_type}
            label={ev.event_type}
            score={ev.payload?.after?.score}
            snapshotUrl={ev.payload?.after?.snapshot_path}
            startedAt={new Date(ev.timestamp)}
            camera={cameraMap[ev.camera_id]}
            isNew
          />
        ))}

        {/* Historical events */}
        {historicalEvents.map(ev => (
          <EventCard
            key={ev.id}
            id={ev.id}
            type={ev.type}
            label={ev.label ?? ev.type}
            score={ev.score}
            snapshotUrl={ev.snapshot_url}
            startedAt={new Date(ev.started_at)}
            camera={cameraMap[ev.camera_id]}
          />
        ))}

        {!eventPage && historicalEvents.length === 0 && (
          <div style={{ color: '#64748b', fontSize: 13, padding: '40px 0', textAlign: 'center' }}>
            Loading events...
          </div>
        )}

        {historicalEvents.length === 0 && liveEvents.length === 0 && eventPage && (
          <div style={{ color: '#64748b', fontSize: 13, padding: '60px 0', textAlign: 'center' }}>
            No events yet. Events will appear here as cameras detect motion and objects.
          </div>
        )}
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div style={{ display: 'flex', justifyContent: 'center', marginTop: 20 }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 6,
            background: '#111624', border: '1px solid #1e2535',
            borderRadius: 10, padding: 6,
          }}>
            <button
              onClick={() => setPage(p => Math.max(0, p - 1))}
              disabled={page === 0}
              style={{
                padding: '6px 10px', borderRadius: 6, fontSize: 12,
                background: page === 0 ? '#141827' : '#1e2535',
                border: '1px solid #1e2535',
                color: page === 0 ? '#475569' : '#94a3b8',
                cursor: page === 0 ? 'not-allowed' : 'pointer',
              }}
            >
              Prev
            </button>

            {Array.from({ length: totalPages }, (_, i) => i).slice(0, 7).map(p => (

            {pageNumbers.map(p => (

              <button
                key={p}
                onClick={() => setPage(p)}
                style={{
                  width: 30, height: 30, borderRadius: 6, fontSize: 12,
                  background: page === p ? '#1e3a5f' : '#1e2535',
                  border: '1px solid #1e2535',
                  color: page === p ? '#93c5fd' : '#94a3b8',
                  cursor: 'pointer',
                }}
              >
                {p + 1}
              </button>
            ))}

            {totalPages > 7 && (

            {totalPages > pageNumbers.length && (

              <span style={{ color: '#64748b', fontSize: 12, padding: '0 6px' }}>…</span>
            )}
            <button
              onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))}
              disabled={page >= totalPages - 1}
              style={{
                padding: '6px 10px', borderRadius: 6, fontSize: 12,
                background: page >= totalPages - 1 ? '#141827' : '#1e2535',
                border: '1px solid #1e2535',
                color: page >= totalPages - 1 ? '#475569' : '#94a3b8',
                cursor: page >= totalPages - 1 ? 'not-allowed' : 'pointer',
              }}
            >
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

const LABEL_COLORS: Record<string, [string, string]> = {
  person:   ['#1e3a5f', '#60a5fa'],
  car:      ['#1c3a2a', '#4ade80'],
  motion:   ['#2d2a1a', '#fbbf24'],
  bicycle:  ['#2d1f40', '#a78bfa'],
  motorcycle: ['#2a1c1c', '#f87171'],
}

function EventCard({ id, type, label, score, snapshotUrl, startedAt, camera, isNew }: {
  id: string
  type: string
  label: string
  score?: number
  snapshotUrl?: string
  startedAt: Date
  camera?: Camera
  isNew?: boolean
}) {
  const [bg, fg] = LABEL_COLORS[label] ?? ['#1e2535', '#94a3b8']

  return (
    <div style={{
      display: 'flex', gap: 14, padding: '12px 16px',
      border: `1px solid ${isNew ? '#1e3a5f' : '#1e2535'}`,
      borderRadius: 8,
      background: isNew ? '#0f1a2e' : '#161b27',
      animation: isNew ? 'fadeIn .3s ease' : 'none',
      alignItems: 'center',
    }}>
      <style>{`@keyframes fadeIn { from { opacity: 0; transform: translateY(-4px) } to { opacity: 1; transform: none } }`}</style>

      {/* Snapshot */}
      <div style={{
        width: 80, height: 52, borderRadius: 6, overflow: 'hidden',
        background: '#0f1117', flexShrink: 0,
      }}>
        {snapshotUrl && (
          <img src={snapshotUrl} alt={label} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
        )}
      </div>

      {/* Info */}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <span style={{
            padding: '2px 8px', borderRadius: 5, fontSize: 11,
            fontWeight: 600, background: bg, color: fg,
            textTransform: 'capitalize',
          }}>
            {label}
          </span>
          {score != null && score > 0 && (
            <span style={{ fontSize: 11, color: '#475569' }}>
              {Math.round(score * 100)}% confidence
            </span>
          )}
          {isNew && (
            <span style={{
              fontSize: 10, fontWeight: 600, color: '#60a5fa',
              padding: '1px 6px', background: '#1e3a5f', borderRadius: 4,
            }}>
              NEW
            </span>
          )}
        </div>
        <div style={{ fontSize: 12, color: '#94a3b8' }}>
          {camera?.name ?? 'Unknown camera'}
          <span style={{ color: '#475569', marginLeft: 8 }}>
            {format(startedAt, 'MMM d, HH:mm:ss')}
            {' · '}
            {formatDistanceToNow(startedAt, { addSuffix: true })}
          </span>
        </div>
      </div>
    </div>
  )
}

function buildPageNumbers(current: number, total: number) {
  const max = 7
  if (total <= max) return Array.from({ length: total }, (_, i) => i)
  let start = Math.max(0, current - 2)
  let end = Math.min(total - 1, start + max - 1)
  if (end - start < max - 1) {
    start = Math.max(0, end - (max - 1))
  }
  return Array.from({ length: end - start + 1 }, (_, i) => start + i)
}
