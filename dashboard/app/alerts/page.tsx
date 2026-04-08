'use client'
import { useState } from 'react'
import useSWR from 'swr'
import { Bell, Plus, Trash2, X, ToggleLeft, ToggleRight } from 'lucide-react'
import { alertRules, sites as sitesAPI } from '@/lib/api'
import type { AlertRule, Camera, Site } from '@/lib/api'

const EVENT_TYPES = ['person', 'car', 'bicycle', 'motion', 'offline']

export default function AlertsPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({
    name: '',
    event_types: ['person'] as string[],
    camera_id: '',
    site_id: '',
    notify_email: '',
    cooldown_secs: 300,
  })
  const [saving, setSaving] = useState(false)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const { data: rules, mutate } = useSWR('alert-rules', () => alertRules.list())
  const { data: siteList } = useSWR('sites-alerts', () => sitesAPI.list())

  const handleCreate = async () => {
    if (!form.name.trim() || form.event_types.length === 0) return
    setSaving(true)
    try {
      await alertRules.create({ name: form.name, event_types: form.event_types })
      await mutate()
      setShowCreate(false)
      setForm({ name: '', event_types: ['person'], camera_id: '', site_id: '', notify_email: '', cooldown_secs: 300 })
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (id: string) => {
    setDeletingId(id)
    try {
      await alertRules.delete(id)
      await mutate()
    } finally {
      setDeletingId(null)
    }
  }

  const toggleType = (type: string) => {
    setForm(f => ({
      ...f,
      event_types: f.event_types.includes(type)
        ? f.event_types.filter(t => t !== type)
        : [...f.event_types, type],
    }))
  }

  return (
    <div style={{ padding: '24px 28px' }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600, color: '#f1f5f9' }}>Alert rules</h1>
          <div style={{ marginTop: 4, fontSize: 13, color: '#64748b' }}>
            {rules?.filter(r => r.enabled).length ?? 0} active rules
          </div>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '8px 16px', borderRadius: 8, fontSize: 13, fontWeight: 500,
            background: '#2563eb', border: 'none', color: 'white', cursor: 'pointer',
          }}
        >
          <Plus size={14} /> New rule
        </button>
      </div>

      {/* Create form */}
      {showCreate && (
        <div style={{
          background: '#1a1f2e', border: '1px solid #2563eb',
          borderRadius: 10, padding: 20, marginBottom: 20,
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
            <span style={{ fontWeight: 500, color: '#f1f5f9', fontSize: 14 }}>New alert rule</span>
            <button onClick={() => setShowCreate(false)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#64748b' }}>
              <X size={16} />
            </button>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, marginBottom: 14 }}>
            <div>
              <Label>Rule name</Label>
              <Input
                value={form.name}
                onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="After-hours person detection"
              />
            </div>
            <div>
              <Label>Cooldown (seconds)</Label>
              <Input
                type="number"
                value={form.cooldown_secs}
                onChange={e => setForm(f => ({ ...f, cooldown_secs: +e.target.value }))}
              />
            </div>
          </div>

          {/* Event types */}
          <div style={{ marginBottom: 14 }}>
            <Label>Trigger on</Label>
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 4 }}>
              {EVENT_TYPES.map(t => (
                <button
                  key={t}
                  onClick={() => toggleType(t)}
                  style={{
                    padding: '5px 12px', borderRadius: 20, fontSize: 12, cursor: 'pointer',
                    textTransform: 'capitalize',
                    background: form.event_types.includes(t) ? '#1e3a5f' : '#1e2535',
                    border: `1px solid ${form.event_types.includes(t) ? '#2563eb' : '#1e2535'}`,
                    color: form.event_types.includes(t) ? '#60a5fa' : '#64748b',
                  }}
                >
                  {t}
                </button>
              ))}
            </div>
          </div>

          {/* Notify email */}
          <div style={{ marginBottom: 14 }}>
            <Label>Notify email (optional)</Label>
            <Input
              value={form.notify_email}
              onChange={e => setForm(f => ({ ...f, notify_email: e.target.value }))}
              placeholder="ops@yourcompany.com"
              type="email"
            />
          </div>

          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={handleCreate}
              disabled={saving || !form.name.trim() || form.event_types.length === 0}
              style={{
                padding: '7px 18px', borderRadius: 6, fontSize: 13, fontWeight: 500,
                background: saving ? '#1e3a5f' : '#2563eb', border: 'none',
                color: 'white', cursor: saving ? 'not-allowed' : 'pointer',
              }}
            >
              {saving ? 'Creating...' : 'Create rule'}
            </button>
            <button
              onClick={() => setShowCreate(false)}
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

      {/* Empty state */}
      {(!rules || rules.length === 0) && !showCreate && (
        <div style={{
          textAlign: 'center', padding: '80px 20px',
          border: '1px dashed #1e2535', borderRadius: 12,
        }}>
          <Bell size={32} color="#374151" style={{ marginBottom: 12 }} />
          <div style={{ color: '#94a3b8', fontSize: 15, fontWeight: 500, marginBottom: 6 }}>No alert rules</div>
          <div style={{ color: '#64748b', fontSize: 13 }}>
            Create a rule to get notified when cameras detect people, vehicles, or motion.
          </div>
        </div>
      )}

      {/* Rules list */}
      {(rules ?? []).length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {(rules ?? []).map(rule => (
            <RuleCard
              key={rule.id}
              rule={rule}
              onDelete={() => handleDelete(rule.id)}
              deleting={deletingId === rule.id}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function RuleCard({ rule, onDelete, deleting }: {
  rule: AlertRule
  onDelete: () => void
  deleting: boolean
}) {
  const typeColors: Record<string, [string, string]> = {
    person: ['#1e3a5f', '#60a5fa'],
    car:    ['#1c3a2a', '#4ade80'],
    motion: ['#2d2a1a', '#fbbf24'],
    offline:['#2a1f1f', '#f87171'],
  }

  return (
    <div style={{
      background: '#161b27', border: `1px solid ${rule.enabled ? '#1e2535' : '#1e2535'}`,
      borderRadius: 10, padding: '14px 18px',
      display: 'flex', alignItems: 'center', gap: 14,
      opacity: rule.enabled ? 1 : 0.55,
    }}>
      <div style={{
        width: 36, height: 36, borderRadius: 8, flexShrink: 0,
        background: rule.enabled ? '#1e3a5f' : '#1e2535',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}>
        <Bell size={15} color={rule.enabled ? '#60a5fa' : '#64748b'} />
      </div>

      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontWeight: 500, fontSize: 13, color: '#f1f5f9', marginBottom: 4 }}>
          {rule.name}
        </div>
        <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          {rule.event_types.map(t => {
            const [bg, fg] = typeColors[t] ?? ['#1e2535', '#94a3b8']
            return (
              <span key={t} style={{
                padding: '1px 7px', borderRadius: 4, fontSize: 10,
                fontWeight: 600, background: bg, color: fg,
                textTransform: 'capitalize',
              }}>
                {t}
              </span>
            )
          })}
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{
          fontSize: 11, padding: '2px 8px', borderRadius: 10,
          background: rule.enabled ? '#14532d' : '#1e2535',
          color: rule.enabled ? '#4ade80' : '#64748b',
          fontWeight: 500,
        }}>
          {rule.enabled ? 'Active' : 'Paused'}
        </span>
        <button
          onClick={onDelete}
          disabled={deleting}
          style={{
            padding: 6, borderRadius: 6, background: '#1e2535',
            border: 'none', cursor: deleting ? 'not-allowed' : 'pointer',
            color: '#ef4444', display: 'flex', alignItems: 'center',
          }}
        >
          <Trash2 size={13} />
        </button>
      </div>
    </div>
  )
}

function Label({ children }: { children: React.ReactNode }) {
  return (
    <label style={{
      display: 'block', fontSize: 11, color: '#64748b',
      marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.05em',
    }}>
      {children}
    </label>
  )
}

function Input({ ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      style={{
        width: '100%', padding: '8px 10px', borderRadius: 6,
        background: '#0f1117', border: '1px solid #1e2535',
        color: '#e2e8f0', fontSize: 13, outline: 'none',
        boxSizing: 'border-box',
        ...props.style,
      }}
    />
  )
}
