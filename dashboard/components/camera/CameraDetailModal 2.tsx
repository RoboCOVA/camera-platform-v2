'use client'
import { useState, useEffect, useRef, useCallback } from 'react'
import { X, ChevronLeft, ChevronRight, Download, Clock } from 'lucide-react'
import useSWR from 'swr'
import { cameras as cameraAPI, events as eventsAPI } from '@/lib/api'
import type { Camera, CamEvent } from '@/lib/api'
import { CameraPlayer } from './CameraPlayer'
import { format, subHours, startOfHour } from 'date-fns'

interface Props {
  camera: Camera
  onClose: () => void
}

type ViewMode = 'live' | 'playback'

export function CameraDetailModal({ camera, onClose }: Props) {
  const [mode, setMode] = useState<ViewMode>('live')
  const [playbackTime, setPlaybackTime] = useState<Date>(new Date())
  const overlayRef = useRef<HTMLDivElement>(null)

  const { data: eventList } = useSWR(
    ['events', camera.id],
    () => eventsAPI.list({ camera_id: camera.id }),
    { refreshInterval: 10_000 }
  )

  // Close on Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [onClose])

  // Close on backdrop click
  const handleBackdrop = useCallback((e: React.MouseEvent) => {
    if (e.target === overlayRef.current) onClose()
  }, [onClose])

  const playbackUrl = mode === 'playback'
    ? `${cameraAPI.streamUrl(camera.id).replace('live.m3u8', '')}${format(playbackTime, 'yyyy-MM-dd/HH')}/index.m3u8`
    : cameraAPI.streamUrl(camera.id)

  return (
    <div
      ref={overlayRef}
      onClick={handleBackdrop}
      style={{
        position: 'fixed', inset: 0, zIndex: 50,
        background: 'rgba(0,0,0,0.85)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        padding: 20,
      }}
    >
      <div style={{
        background: '#161b27',
        border: '1px solid #1e2535',
        borderRadius: 14,
        width: '100%',
        maxWidth: 1100,
        maxHeight: '90vh',
        overflow: 'hidden',
        display: 'flex',
        flexDirection: 'column',
      }}>
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '14px 20px',
          borderBottom: '1px solid #1e2535',
          flexShrink: 0,
        }}>
          <div>
            <div style={{ fontWeight: 600, fontSize: 15, color: '#f1f5f9' }}>{camera.name}</div>
            <div style={{ fontSize: 11, color: '#64748b', marginTop: 2 }}>
              {camera.manufacturer} {camera.model} · {camera.ip} · {camera.width}×{camera.height}
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {/* Mode toggle */}
            <div style={{ display: 'flex', borderRadius: 8, border: '1px solid #1e2535', overflow: 'hidden' }}>
              {(['live', 'playback'] as const).map(m => (
                <button
                  key={m}
                  onClick={() => setMode(m)}
                  style={{
                    padding: '5px 14px', fontSize: 12, cursor: 'pointer',
                    background: mode === m ? '#1e3a5f' : 'transparent',
                    border: 'none',
                    color: mode === m ? '#60a5fa' : '#64748b',
                    fontWeight: mode === m ? 500 : 400,
                    textTransform: 'capitalize',
                  }}
                >
                  {m === 'live' ? '● Live' : '⏮ Playback'}
                </button>
              ))}
            </div>
            <button
              onClick={onClose}
              style={{
                padding: 6, borderRadius: 8, background: '#1e2535',
                border: 'none', cursor: 'pointer', color: '#94a3b8',
                display: 'flex', alignItems: 'center',
              }}
            >
              <X size={16} />
            </button>
          </div>
        </div>

        {/* Body: player + sidebar */}
        <div style={{ display: 'flex', flex: 1, overflow: 'hidden', minHeight: 0 }}>
          {/* Player */}
          <div style={{ flex: 1, padding: 16, display: 'flex', flexDirection: 'column', gap: 12, minWidth: 0 }}>
            <CameraPlayer
              cameraId={camera.id}
              streamUrl={playbackUrl}
              snapshotUrl={cameraAPI.snapshotUrl(camera.id)}
              name={camera.name}
              status={camera.status}
              showLabel={false}
            />

            {/* Playback timeline */}
            {mode === 'playback' && (
              <Timeline
                currentTime={playbackTime}
                events={eventList ?? []}
                onChange={setPlaybackTime}
              />
            )}
          </div>

          {/* Event sidebar */}
          <aside style={{
            width: 280, flexShrink: 0,
            borderLeft: '1px solid #1e2535',
            display: 'flex', flexDirection: 'column',
            overflowY: 'auto',
          }}>
            <div style={{
              padding: '12px 16px', fontSize: 11, fontWeight: 500,
              color: '#64748b', textTransform: 'uppercase',
              letterSpacing: '0.05em', borderBottom: '1px solid #1e2535',
              position: 'sticky', top: 0, background: '#161b27', zIndex: 1,
            }}>
              Recent events
            </div>
            {(eventList ?? []).length === 0 && (
              <div style={{ padding: 20, color: '#64748b', fontSize: 13, textAlign: 'center' }}>
                No events recorded
              </div>
            )}
            {(eventList ?? []).map(ev => (
              <EventRow
                key={ev.id}
                event={ev}
                onSeek={(t) => { setMode('playback'); setPlaybackTime(t) }}
              />
            ))}
          </aside>
        </div>
      </div>
    </div>
  )
}

// ─── Timeline component ───────────────────────────────────────────────────────

function Timeline({ currentTime, events, onChange }: {
  currentTime: Date
  events: CamEvent[]
  onChange: (d: Date) => void
}) {
  const hours = Array.from({ length: 24 }, (_, i) =>
    subHours(startOfHour(new Date()), 23 - i)
  )

  return (
    <div style={{
      background: '#0f1117',
      borderRadius: 8,
      padding: '10px 12px',
      border: '1px solid #1e2535',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <Clock size={13} color="#64748b" />
        <span style={{ fontSize: 12, color: '#64748b' }}>
          {format(currentTime, 'MMM d, HH:mm')}
        </span>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 4 }}>
          <button
            onClick={() => onChange(new Date(currentTime.getTime() - 3600_000))}
            style={{ padding: 4, background: '#1e2535', border: 'none', borderRadius: 4, cursor: 'pointer', color: '#94a3b8' }}
          >
            <ChevronLeft size={12} />
          </button>
          <button
            onClick={() => onChange(new Date(currentTime.getTime() + 3600_000))}
            style={{ padding: 4, background: '#1e2535', border: 'none', borderRadius: 4, cursor: 'pointer', color: '#94a3b8' }}
          >
            <ChevronRight size={12} />
          </button>
        </div>
      </div>

      {/* 24-hour bar */}
      <div style={{ display: 'flex', gap: 2, height: 28, alignItems: 'stretch' }}>
        {hours.map((h, i) => {
          const hasEvent = events.some(ev => {
            const evH = startOfHour(new Date(ev.started_at)).getTime()
            return evH === h.getTime()
          })
          const isCurrent = startOfHour(currentTime).getTime() === h.getTime()
          return (
            <button
              key={i}
              onClick={() => onChange(h)}
              title={format(h, 'HH:00')}
              style={{
                flex: 1, borderRadius: 3, border: 'none', cursor: 'pointer',
                background: isCurrent
                  ? '#2563eb'
                  : hasEvent ? '#1e3a5f' : '#1e2535',
                minWidth: 0,
                position: 'relative',
              }}
            >
              {hasEvent && !isCurrent && (
                <div style={{
                  position: 'absolute', top: 3, left: '50%',
                  transform: 'translateX(-50%)',
                  width: 4, height: 4, borderRadius: '50%',
                  background: '#60a5fa',
                }} />
              )}
            </button>
          )
        })}
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 4 }}>
        <span style={{ fontSize: 9, color: '#475569' }}>24h ago</span>
        <span style={{ fontSize: 9, color: '#475569' }}>now</span>
      </div>
    </div>
  )
}

// ─── Event row ────────────────────────────────────────────────────────────────

function EventRow({ event, onSeek }: { event: CamEvent; onSeek: (t: Date) => void }) {
  const eventColors: Record<string, string> = {
    person: '#60a5fa',
    car: '#4ade80',
    motion: '#fbbf24',
    bicycle: '#a78bfa',
  }
  const color = eventColors[event.label ?? event.type] ?? '#94a3b8'

  return (
    <div
      onClick={() => onSeek(new Date(event.started_at))}
      style={{
        display: 'flex', gap: 10, padding: '10px 16px',
        borderBottom: '1px solid #1e2535', cursor: 'pointer',
        transition: 'background .1s',
      }}
      onMouseEnter={e => (e.currentTarget.style.background = '#1a1f2e')}
      onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
    >
      {/* Snapshot thumbnail */}
      <div style={{
        width: 60, height: 40, borderRadius: 4, overflow: 'hidden',
        background: '#0f1117', flexShrink: 0,
      }}>
        {event.snapshot_url && (
          <img
            src={event.snapshot_url}
            alt={event.type}
            style={{ width: '100%', height: '100%', objectFit: 'cover' }}
          />
        )}
      </div>

      {/* Details */}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ color, fontWeight: 500, fontSize: 12, textTransform: 'capitalize' }}>
            {event.label ?? event.type}
          </span>
          {event.score > 0 && (
            <span style={{ fontSize: 10, color: '#475569' }}>
              {Math.round(event.score * 100)}%
            </span>
          )}
        </div>
        <div style={{ fontSize: 11, color: '#64748b', marginTop: 2 }}>
          {format(new Date(event.started_at), 'MMM d, HH:mm:ss')}
        </div>
      </div>
    </div>
  )
}
