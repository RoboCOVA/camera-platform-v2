'use client'
import { useState, useEffect, useCallback } from 'react'
import { useSession } from 'next-auth/react'
import useSWR from 'swr'
import { RefreshCw, WifiOff, LayoutGrid, List, Filter } from 'lucide-react'
import { cameras as cameraAPI, sites as sitesAPI, connectEventStream } from '@/lib/api'
import type { Camera, Site, WSEvent } from '@/lib/api'
import { CameraPlayer } from '@/components/camera/CameraPlayer'
import { CameraDetailModal } from '@/components/camera/CameraDetailModal'
import { formatDistanceToNow } from 'date-fns'

const fetcher = (fn: () => Promise<any>) => fn()

export default function CamerasPage() {
  const { data: session } = useSession()
  const [selectedSite, setSelectedSite] = useState<string>('all')
  const [selectedCamera, setSelectedCamera] = useState<Camera | null>(null)
  const [layout, setLayout] = useState<'grid' | 'list'>('grid')
  const [liveStatuses, setLiveStatuses] = useState<Record<string, 'online' | 'offline'>>({})
  const [wsConnected, setWsConnected] = useState(false)
  const [recentEvents, setRecentEvents] = useState<Record<string, WSEvent>>({})

  const { data: cameraList, isLoading: camsLoading, mutate: mutateCameras } =
    useSWR(['cameras', selectedSite],
      () => cameraAPI.list(selectedSite === 'all' ? undefined : selectedSite),
      { refreshInterval: 30_000 }
    )

  const { data: siteList } = useSWR('sites', () => sitesAPI.list())

  // Real-time WebSocket events
  useEffect(() => {
    if (!session?.accessToken) return

    const disconnect = connectEventStream(
      session.accessToken,
      (event: WSEvent) => {
        // Update live status
        if (event.event_type === 'offline') {
          setLiveStatuses(s => ({ ...s, [event.camera_id]: 'offline' }))
        } else {
          setLiveStatuses(s => ({ ...s, [event.camera_id]: 'online' }))
        }
        // Track most recent event per camera
        setRecentEvents(e => ({ ...e, [event.camera_id]: event }))
      },
      setWsConnected
    )
    return disconnect
  }, [session?.accessToken])

  const cameras = cameraList ?? []
  const sites = siteList ?? []

  const filteredCameras = selectedSite === 'all'
    ? cameras
    : cameras.filter(c => c.site_id === selectedSite)

  const onlineCount = cameras.filter(c =>
    (liveStatuses[c.id] ?? c.status) === 'online'
  ).length

  return (
    <div style={{ padding: '24px 28px', minHeight: '100vh' }}>

      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600, color: '#f1f5f9' }}>
            Cameras
          </h1>
          <div style={{ marginTop: 4, fontSize: 13, color: '#64748b' }}>
            {onlineCount} online · {cameras.length - onlineCount} offline · {cameras.length} total
            {wsConnected
              ? <span style={{ marginLeft: 10, color: '#22c55e', fontSize: 11 }}>● Live</span>
              : <span style={{ marginLeft: 10, color: '#64748b', fontSize: 11 }}>○ Connecting...</span>
            }
          </div>
        </div>

        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {/* Layout toggle */}
          <div style={{
            display: 'flex', borderRadius: 8, overflow: 'hidden',
            border: '1px solid #1e2535',
          }}>
            {(['grid', 'list'] as const).map(l => (
              <button
                key={l}
                onClick={() => setLayout(l)}
                style={{
                  padding: '6px 12px', background: layout === l ? '#1e3a5f' : 'transparent',
                  border: 'none', cursor: 'pointer',
                  color: layout === l ? '#60a5fa' : '#64748b',
                }}
              >
                {l === 'grid' ? <LayoutGrid size={15} /> : <List size={15} />}
              </button>
            ))}
          </div>

          <button
            onClick={() => mutateCameras()}
            style={{
              padding: '6px 12px', borderRadius: 8, background: '#1e2535',
              border: '1px solid #1e2535', color: '#94a3b8', cursor: 'pointer',
              display: 'flex', alignItems: 'center', gap: 6, fontSize: 13,
            }}
          >
            <RefreshCw size={14} /> Refresh
          </button>
        </div>
      </div>

      {/* Site filter pills */}
      {sites.length > 0 && (
        <div style={{ display: 'flex', gap: 6, marginBottom: 20, flexWrap: 'wrap' }}>
          {[{ id: 'all', name: 'All sites' }, ...sites].map(site => (
            <button
              key={site.id}
              onClick={() => setSelectedSite(site.id)}
              style={{
                padding: '5px 14px', borderRadius: 20, fontSize: 12, cursor: 'pointer',
                background: selectedSite === site.id ? '#1e3a5f' : '#1a1f2e',
                border: `1px solid ${selectedSite === site.id ? '#2563eb' : '#1e2535'}`,
                color: selectedSite === site.id ? '#60a5fa' : '#94a3b8',
                fontWeight: selectedSite === site.id ? 500 : 400,
              }}
            >
              {site.name}
              {site.id !== 'all' && (
                <span style={{ marginLeft: 6, opacity: 0.6 }}>
                  {cameras.filter(c => c.site_id === site.id).length}
                </span>
              )}
            </button>
          ))}
        </div>
      )}

      {/* Loading state */}
      {camsLoading && (
        <div style={{ color: '#64748b', fontSize: 13, padding: '40px 0', textAlign: 'center' }}>
          Loading cameras...
        </div>
      )}

      {/* Empty state */}
      {!camsLoading && filteredCameras.length === 0 && (
        <EmptyState />
      )}

      {/* Camera grid */}
      {layout === 'grid' && filteredCameras.length > 0 && (
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
          gap: 14,
        }}>
          {filteredCameras.map(cam => {
            const liveStatus = liveStatuses[cam.id] ?? cam.status
            const lastEvent = recentEvents[cam.id]
            return (
              <div key={cam.id}>
                <CameraPlayer
                  cameraId={cam.id}
                  streamUrl={cameraAPI.streamUrl(cam.id)}
                  snapshotUrl={cameraAPI.snapshotUrl(cam.id)}
                  name={cam.name}
                  status={liveStatus as any}
                  onClick={() => setSelectedCamera(cam)}
                />
                {lastEvent && (
                  <div style={{
                    marginTop: 4, paddingLeft: 2,
                    fontSize: 11, color: '#64748b',
                    display: 'flex', gap: 6,
                  }}>
                    <EventBadge type={lastEvent.event_type} />
                    <span>
                      {formatDistanceToNow(new Date(lastEvent.timestamp), { addSuffix: true })}
                    </span>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* Camera list view */}
      {layout === 'list' && filteredCameras.length > 0 && (
        <CameraListView
          cameras={filteredCameras}
          liveStatuses={liveStatuses}
          recentEvents={recentEvents}
          onSelect={setSelectedCamera}
        />
      )}

      {/* Detail modal */}
      {selectedCamera && (
        <CameraDetailModal
          camera={selectedCamera}
          onClose={() => setSelectedCamera(null)}
        />
      )}
    </div>
  )
}

// ─── Camera list view ─────────────────────────────────────────────────────────

function CameraListView({ cameras, liveStatuses, recentEvents, onSelect }: {
  cameras: Camera[]
  liveStatuses: Record<string, 'online' | 'offline'>
  recentEvents: Record<string, WSEvent>
  onSelect: (c: Camera) => void
}) {
  return (
    <div style={{ border: '1px solid #1e2535', borderRadius: 10, overflow: 'hidden' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr style={{ background: '#161b27', borderBottom: '1px solid #1e2535' }}>
            {['Name', 'Status', 'Model', 'IP', 'Last event'].map(h => (
              <th key={h} style={{
                padding: '10px 16px', textAlign: 'left',
                fontSize: 11, fontWeight: 500, color: '#64748b',
                textTransform: 'uppercase', letterSpacing: '0.05em',
              }}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {cameras.map((cam, i) => {
            const status = liveStatuses[cam.id] ?? cam.status
            const lastEvent = recentEvents[cam.id]
            return (
              <tr
                key={cam.id}
                onClick={() => onSelect(cam)}
                style={{
                  borderBottom: i < cameras.length - 1 ? '1px solid #1e2535' : 'none',
                  cursor: 'pointer',
                  transition: 'background .1s',
                }}
                onMouseEnter={e => (e.currentTarget.style.background = '#161b27')}
                onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
              >
                <td style={{ padding: '12px 16px', color: '#e2e8f0', fontSize: 13, fontWeight: 500 }}>
                  {cam.name}
                </td>
                <td style={{ padding: '12px 16px' }}>
                  <StatusDot status={status as any} />
                </td>
                <td style={{ padding: '12px 16px', color: '#94a3b8', fontSize: 13 }}>
                  {cam.manufacturer} {cam.model}
                </td>
                <td style={{ padding: '12px 16px', color: '#64748b', fontSize: 12, fontFamily: 'monospace' }}>
                  {cam.ip}
                </td>
                <td style={{ padding: '12px 16px', fontSize: 12, color: '#64748b' }}>
                  {lastEvent ? (
                    <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      <EventBadge type={lastEvent.event_type} />
                      {formatDistanceToNow(new Date(lastEvent.timestamp), { addSuffix: true })}
                    </span>
                  ) : (
                    cam.last_seen
                      ? formatDistanceToNow(new Date(cam.last_seen), { addSuffix: true })
                      : '—'
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

// ─── Small components ─────────────────────────────────────────────────────────

function StatusDot({ status }: { status: 'online' | 'offline' | 'error' }) {
  const color = status === 'online' ? '#22c55e' : status === 'error' ? '#ef4444' : '#6b7280'
  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
      <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, display: 'inline-block' }} />
      <span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{status}</span>
    </span>
  )
}

function EventBadge({ type }: { type: string }) {
  const colors: Record<string, [string, string]> = {
    person:   ['#1e3a5f', '#60a5fa'],
    car:      ['#1c3a2a', '#4ade80'],
    motion:   ['#2d2a1a', '#fbbf24'],
    offline:  ['#2a1f1f', '#f87171'],
  }
  const [bg, fg] = colors[type] ?? ['#1e2535', '#94a3b8']
  return (
    <span style={{
      padding: '1px 6px', borderRadius: 4, fontSize: 10,
      fontWeight: 500, background: bg, color: fg,
      textTransform: 'capitalize',
    }}>
      {type}
    </span>
  )
}

function EmptyState() {
  return (
    <div style={{
      textAlign: 'center', padding: '80px 20px',
      border: '1px dashed #1e2535', borderRadius: 12,
    }}>
      <WifiOff size={32} color="#374151" style={{ marginBottom: 12 }} />
      <div style={{ color: '#94a3b8', fontSize: 15, fontWeight: 500, marginBottom: 8 }}>
        No cameras found
      </div>
      <div style={{ color: '#64748b', fontSize: 13, lineHeight: 1.6 }}>
        Install the edge agent on a mini-PC connected to your camera network.<br />
        Cameras will appear here automatically within 60 seconds.
      </div>
    </div>
  )
}
