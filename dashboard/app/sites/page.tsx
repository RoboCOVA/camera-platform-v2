'use client'
import { useState } from 'react'
import useSWR from 'swr'
import { MapPin, Plus, X } from 'lucide-react'
import { sites as sitesAPI, cameras as cameraAPI } from '@/lib/api'

export default function SitesPage() {
  const [showAdd, setShowAdd] = useState(false)
  const [form, setForm] = useState({ name: '', address: '', timezone: 'UTC' })
  const [saving, setSaving] = useState(false)

  const { data: siteList, mutate } = useSWR('sites-page', () => sitesAPI.list())
  const { data: cameraList } = useSWR('cameras-sites', () => cameraAPI.list())

  const camerasPerSite = Object.fromEntries(
    (siteList ?? []).map(s => [
      s.id,
      (cameraList ?? []).filter(c => c.site_id === s.id),
    ])
  )

  const handleCreate = async () => {
    if (!form.name.trim()) return
    setSaving(true)
    try {
      await sitesAPI.create(form)
      await mutate()
      setShowAdd(false)
      setForm({ name: '', address: '', timezone: 'UTC' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ padding: '24px 28px' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600, color: '#f1f5f9' }}>Sites</h1>
          <div style={{ marginTop: 4, fontSize: 13, color: '#64748b' }}>
            {siteList?.length ?? 0} location{siteList?.length !== 1 ? 's' : ''}
          </div>
        </div>
        <button
          onClick={() => setShowAdd(true)}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '8px 16px', borderRadius: 8, fontSize: 13, fontWeight: 500,
            background: '#2563eb', border: 'none', color: 'white', cursor: 'pointer',
          }}
        >
          <Plus size={14} /> Add site
        </button>
      </div>

      {/* Add site form */}
      {showAdd && (
        <div style={{
          background: '#1a1f2e', border: '1px solid #2563eb',
          borderRadius: 10, padding: 20, marginBottom: 20,
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
            <span style={{ fontWeight: 500, color: '#f1f5f9', fontSize: 14 }}>New site</span>
            <button onClick={() => setShowAdd(false)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#64748b' }}>
              <X size={16} />
            </button>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12, marginBottom: 16 }}>
            {[
              { key: 'name', label: 'Site name', placeholder: 'Main warehouse' },
              { key: 'address', label: 'Address', placeholder: '123 Main St, City' },
              { key: 'timezone', label: 'Timezone', placeholder: 'America/New_York' },
            ].map(({ key, label, placeholder }) => (
              <div key={key}>
                <label style={{ display: 'block', fontSize: 11, color: '#64748b', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                  {label}
                </label>
                <input
                  value={(form as any)[key]}
                  onChange={e => setForm(f => ({ ...f, [key]: e.target.value }))}
                  placeholder={placeholder}
                  style={{
                    width: '100%', padding: '8px 10px', borderRadius: 6,
                    background: '#0f1117', border: '1px solid #1e2535',
                    color: '#e2e8f0', fontSize: 13, outline: 'none',
                    boxSizing: 'border-box',
                  }}
                />
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={handleCreate}
              disabled={saving || !form.name.trim()}
              style={{
                padding: '7px 18px', borderRadius: 6, fontSize: 13, fontWeight: 500,
                background: saving ? '#1e3a5f' : '#2563eb', border: 'none',
                color: 'white', cursor: saving ? 'not-allowed' : 'pointer',
              }}
            >
              {saving ? 'Creating...' : 'Create site'}
            </button>
            <button
              onClick={() => setShowAdd(false)}
              style={{
                padding: '7px 18px', borderRadius: 6, fontSize: 13,
                background: '#1e2535', border: 'none', color: '#94a3b8', cursor: 'pointer',
              }}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Sites grid */}
      {(!siteList || siteList.length === 0) && !showAdd && (
        <div style={{
          textAlign: 'center', padding: '80px 20px',
          border: '1px dashed #1e2535', borderRadius: 12,
        }}>
          <MapPin size={32} color="#374151" style={{ marginBottom: 12 }} />
          <div style={{ color: '#94a3b8', fontSize: 15, fontWeight: 500, marginBottom: 6 }}>No sites yet</div>
          <div style={{ color: '#64748b', fontSize: 13 }}>
            Sites group cameras by physical location.
          </div>
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: 14 }}>
        {(siteList ?? []).map(site => {
          const siteCameras = camerasPerSite[site.id] ?? []
          const online = siteCameras.filter(c => c.status === 'online').length
          return (
            <div key={site.id} style={{
              background: '#161b27', border: '1px solid #1e2535',
              borderRadius: 10, padding: 18,
            }}>
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
                <div style={{
                  width: 36, height: 36, borderRadius: 8,
                  background: '#1e3a5f', display: 'flex', alignItems: 'center', justifyContent: 'center',
                  flexShrink: 0,
                }}>
                  <MapPin size={16} color="#60a5fa" />
                </div>
                <div>
                  <div style={{ fontWeight: 500, fontSize: 14, color: '#f1f5f9' }}>{site.name}</div>
                  {site.address && (
                    <div style={{ fontSize: 12, color: '#64748b', marginTop: 2 }}>{site.address}</div>
                  )}
                </div>
              </div>
              <div style={{ display: 'flex', gap: 16, marginTop: 14, paddingTop: 14, borderTop: '1px solid #1e2535' }}>
                <div>
                  <div style={{ fontSize: 18, fontWeight: 600, color: '#f1f5f9' }}>{siteCameras.length}</div>
                  <div style={{ fontSize: 11, color: '#64748b' }}>cameras</div>
                </div>
                <div>
                  <div style={{ fontSize: 18, fontWeight: 600, color: '#22c55e' }}>{online}</div>
                  <div style={{ fontSize: 11, color: '#64748b' }}>online</div>
                </div>
                <div>
                  <div style={{ fontSize: 18, fontWeight: 600, color: '#94a3b8' }}>{site.timezone}</div>
                  <div style={{ fontSize: 11, color: '#64748b' }}>timezone</div>
                </div>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
