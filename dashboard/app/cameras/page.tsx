'use client'
import { useState, useEffect, CSSProperties } from 'react'
import { useSession } from 'next-auth/react'
import useSWR from 'swr'
import { RefreshCw, LayoutGrid, List } from 'lucide-react'
import { cameras as cameraAPI, sites as sitesAPI, connectEventStream } from '@/lib/api'
import type { Camera, Site, WSEvent } from '@/lib/api'
import { CameraPlayer } from '@/components/camera/CameraPlayer'
import { CameraDetailModal } from '@/components/camera/CameraDetailModal'
import { formatDistanceToNow } from 'date-fns'

const PAGE_SIZE = 50

export default function CamerasPage() {
  const { data: session } = useSession()
  const [selectedSite, setSelectedSite] = useState<string>('all')
  const [selectedCamera, setSelectedCamera] = useState<Camera | null>(null)
  const [layout, setLayout] = useState<'grid' | 'list'>('grid')
  const [liveStatuses, setLiveStatuses] = useState<Record<string, 'online' | 'offline'>>({})
  const [wsConnected, setWsConnected] = useState(false)
  const [recentEvents, setRecentEvents] = useState<Record<string, WSEvent>>({})
  const [page, setPage] = useState(0)
  const [showCreate, setShowCreate] = useState(false)

  const { data: cameraPage, isLoading: camsLoading, mutate: mutateCameras } =
    useSWR(['cameras', selectedSite, page],
      () => cameraAPI.list(selectedSite === 'all' ? undefined : selectedSite, {
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      }),
      { refreshInterval: page === 0 ? 30_000 : 0 }
    )

  const { data: siteList } = useSWR('sites', () => sitesAPI.list())

  // Real-time WebSocket events
  useEffect(() => {
    if (!session?.accessToken) return

    const disconnect = connectEventStream(
      session.accessToken,
      (event: WSEvent) => {
        if (event.event_type === 'offline') {
          setLiveStatuses(s => ({ ...s, [event.camera_id]: 'offline' }))
        } else {
          setLiveStatuses(s => ({ ...s, [event.camera_id]: 'online' }))
        }
        setRecentEvents(e => ({ ...e, [event.camera_id]: event }))
      },
      setWsConnected
    )
    return disconnect
  }, [session?.accessToken])

  useEffect(() => {
    setPage(0)
  }, [selectedSite])

  const cameras = cameraPage?.items ?? []

  const total = cameraPage?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const pageNumbers = buildPageNumbers(page, totalPages)

  useEffect(() => {
    if (page > totalPages - 1) setPage(0)
  }, [page, totalPages])

>>>>>>> fdc694e (added guides doc)
  const sites = siteList ?? []
  const total = cameraPage?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const filteredCameras = selectedSite === 'all'
    ? cameras
    : cameras.filter(c => c.site_id === selectedSite)

  const onlineCount = cameras.filter(c =>
    (liveStatuses[c.id] ?? c.status) === 'online'
  ).length
  const canAdmin = (session?.roles ?? []).some(r => r === 'org-owner' || r === 'org-admin')

  useEffect(() => {
    if (page > totalPages - 1) setPage(0)
  }, [page, totalPages])

  const canAdmin = (session?.roles ?? []).some(r => r === 'org-owner' || r === 'org-admin')

  return (
    <div style={{ padding: '24px 28px', minHeight: '100vh' }}>

      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600, color: '#f1f5f9' }}>
            Cameras
          </h1>
          <div style={{ marginTop: 4, fontSize: 13, color: '#64748b' }}>
            {onlineCount} online · {cameras.length - onlineCount} offline · {cameras.length} shown
            {total > 0 && (
              <span style={{ marginLeft: 8, color: '#475569' }}>
                · {Math.min((page + 1) * PAGE_SIZE, total)} of {total}
              </span>
            )}
            {wsConnected
              ? <span style={{ marginLeft: 10, color: '#22c55e', fontSize: 11 }}>● Live</span>
              : <span style={{ marginLeft: 10, color: '#64748b', fontSize: 11 }}>○ Connecting...</span>
            }
          </div>
        </div>

        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {canAdmin && (
            <button
              onClick={() => setShowCreate(true)}
              style={{
                padding: '6px 12px', borderRadius: 8, background: '#1e3a5f',
                border: '1px solid #2563eb', color: '#93c5fd', cursor: 'pointer',
                fontSize: 13,
              }}
            >
              Add camera
            </button>
          )}
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
            onClick={() => { setPage(0); mutateCameras() }}
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
      {camsLoading && cameras.length === 0 && (
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
=======
            {pageNumbers.map(p => (
>>>>>>> fdc694e (added guides doc)
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
>>>>>>> fdc694e (added guides doc)
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

      {/* Detail modal */}
      {selectedCamera && (
        <CameraDetailModal
          camera={selectedCamera}
          onClose={() => setSelectedCamera(null)}
          canAdmin={canAdmin}
          onDeleted={() => {
            setSelectedCamera(null)
            setPage(0)
            mutateCameras()
          }}
        />
      )}

      {showCreate && (
        <CameraCreateModal
          sites={sites}
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            setPage(0)
            mutateCameras()
          }}
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {cameras.map(cam => {
        const liveStatus = liveStatuses[cam.id] ?? cam.status
        const lastEvent = recentEvents[cam.id]
        return (
          <div
            key={cam.id}
            onClick={() => onSelect(cam)}
            style={{
              padding: 14, borderRadius: 10, border: '1px solid #1e2535',
              background: '#161b27', cursor: 'pointer',
              display: 'flex', alignItems: 'center', gap: 12,
            }}
          >
            <div style={{
              width: 8, height: 8, borderRadius: '50%',
              background: liveStatus === 'online' ? '#22c55e' : '#64748b',
            }} />
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 14, fontWeight: 600, color: '#f1f5f9' }}>{cam.name}</div>
              <div style={{ fontSize: 12, color: '#64748b' }}>
                {cam.manufacturer} {cam.model} · {cam.ip}
              </div>
            </div>
            {lastEvent && (
              <div style={{ fontSize: 11, color: '#64748b' }}>
                {formatDistanceToNow(new Date(lastEvent.timestamp), { addSuffix: true })}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function CameraCreateModal({ sites, onClose, onCreated }: {
  sites: Site[]
  onClose: () => void
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [siteId, setSiteId] = useState<string>('')
  const [ip, setIp] = useState('')
  const [manufacturer, setManufacturer] = useState('')
  const [model, setModel] = useState('')
  const [saving, setSaving] = useState(false)


  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 60,
      background: 'rgba(0,0,0,0.7)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      padding: 20,
    }}>
      <div style={{
        background: '#161b27',
        border: '1px solid #1e2535',
        borderRadius: 12,
        width: '100%',
        maxWidth: 520,
        padding: 18,
      }}>
        <div style={{ fontSize: 16, fontWeight: 600, color: '#f1f5f9', marginBottom: 12 }}>
          Add camera
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder="Camera name"
            style={inputStyle}
          />
          <select
            value={siteId}
            onChange={e => setSiteId(e.target.value)}
            style={inputStyle}
          >
            <option value="">No site</option>
            {sites.map(s => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
          <input
            value={ip}
            onChange={e => setIp(e.target.value)}
            placeholder="IP address (optional)"
            style={inputStyle}
          />
          <input
            value={manufacturer}
            onChange={e => setManufacturer(e.target.value)}
            placeholder="Manufacturer (optional)"
            style={inputStyle}
          />
          <input
            value={model}
            onChange={e => setModel(e.target.value)}
            placeholder="Model (optional)"
            style={inputStyle}
          />
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button
            onClick={onClose}
            style={{
              padding: '6px 12px', borderRadius: 8, background: '#1e2535',
              border: '1px solid #1e2535', color: '#94a3b8', cursor: 'pointer',
              fontSize: 13,
            }}
          >
            Cancel
          </button>
          <button
            onClick={async () => {
              if (!name.trim()) return
              setSaving(true)
              try {
                await cameraAPI.create({
                  name: name.trim(),
                  site_id: siteId || undefined,
                  ip: ip || undefined,
                  manufacturer: manufacturer || undefined,
                  model: model || undefined,
                } as any)
                onCreated()
              } finally {
                setSaving(false)
              }
            }}
            disabled={saving || !name.trim()}
            style={{
              padding: '6px 12px', borderRadius: 8,
              background: saving || !name.trim() ? '#1a2230' : '#1e3a5f',
              border: '1px solid #2563eb', color: '#93c5fd',
              cursor: saving || !name.trim() ? 'not-allowed' : 'pointer',
              fontSize: 13,
            }}
          >
            {saving ? 'Saving...' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  )
}

const inputStyle: CSSProperties = {
  padding: '8px 10px',
  borderRadius: 8,
  border: '1px solid #1e2535',
  background: '#0f1117',
  color: '#e2e8f0',
  fontSize: 13,
}

// ─── Small components ─────────────────────────────────────────────────────────
=======
>>>>>>> fdc694e (added guides doc)

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 60,
      background: 'rgba(0,0,0,0.7)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      padding: 20,
    }}>
      <div style={{
        background: '#161b27',
        border: '1px solid #1e2535',
        borderRadius: 12,
        width: '100%',
        maxWidth: 520,
        padding: 18,
      }}>
        <div style={{ fontSize: 16, fontWeight: 600, color: '#f1f5f9', marginBottom: 12 }}>
          Add camera
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder="Camera name"
            style={inputStyle}
          />
          <select
            value={siteId}
            onChange={e => setSiteId(e.target.value)}
            style={inputStyle}
          >
            <option value="">No site</option>
            {sites.map(s => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
          <input
            value={ip}
            onChange={e => setIp(e.target.value)}
            placeholder="IP address (optional)"
            style={inputStyle}
          />
          <input
            value={manufacturer}
            onChange={e => setManufacturer(e.target.value)}
            placeholder="Manufacturer (optional)"
            style={inputStyle}
          />
          <input
            value={model}
            onChange={e => setModel(e.target.value)}
            placeholder="Model (optional)"
            style={inputStyle}
          />
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button
            onClick={onClose}
            style={{
              padding: '6px 12px', borderRadius: 8, background: '#1e2535',
              border: '1px solid #1e2535', color: '#94a3b8', cursor: 'pointer',
              fontSize: 13,
            }}
          >
            Cancel
          </button>
          <button
            onClick={async () => {
              if (!name.trim()) return
              setSaving(true)
              try {
                await cameraAPI.create({
                  name: name.trim(),
                  site_id: siteId || undefined,
                  ip: ip || undefined,
                  manufacturer: manufacturer || undefined,
                  model: model || undefined,
                } as any)
                onCreated()
              } finally {
                setSaving(false)
              }
            }}
            disabled={saving || !name.trim()}
            style={{
              padding: '6px 12px', borderRadius: 8,
              background: saving || !name.trim() ? '#1a2230' : '#1e3a5f',
              border: '1px solid #2563eb', color: '#93c5fd',
              cursor: saving || !name.trim() ? 'not-allowed' : 'pointer',
              fontSize: 13,
            }}
          >
            {saving ? 'Saving...' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  )
}

const inputStyle: CSSProperties = {
  padding: '8px 10px',
  borderRadius: 8,
  border: '1px solid #1e2535',
  background: '#0f1117',
  color: '#e2e8f0',
  fontSize: 13,
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

function EmptyState() {
  return (
    <div style={{ textAlign: 'center', padding: '80px 20px', color: '#64748b' }}>
      <div style={{ fontSize: 14, fontWeight: 500 }}>No cameras found</div>
      <div style={{ fontSize: 12, marginTop: 6 }}>Add a camera to start monitoring.</div>
    </div>
  )
}

function EventBadge({ type }: { type: string }) {
  const colors: Record<string, string> = {
    person: '#60a5fa',
    car: '#4ade80',
    motion: '#fbbf24',
    bicycle: '#a78bfa',
    offline: '#f87171',
  }
  return (
    <span style={{
      padding: '2px 6px', borderRadius: 4, fontSize: 10,
      color: colors[type] ?? '#94a3b8', border: `1px solid ${colors[type] ?? '#334155'}`,
    }}>
      {type}
    </span>
  )
}
