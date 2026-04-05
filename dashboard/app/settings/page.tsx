'use client'
import { useState } from 'react'
import useSWR from 'swr'
import { useSession } from 'next-auth/react'
import { Copy, Check, RefreshCw, Terminal, User, Shield } from 'lucide-react'
import { org as orgAPI } from '@/lib/api'

export default function SettingsPage() {
  const { data: session } = useSession()
  const { data: orgData } = useSWR('org-me', () => orgAPI.me())
  const [activeTab, setActiveTab] = useState<'org' | 'devices' | 'account'>('org')

  return (
    <div style={{ padding: '24px 28px' }}>
      <h1 style={{ margin: '0 0 24px', fontSize: 22, fontWeight: 600, color: '#f1f5f9' }}>
        Settings
      </h1>

      {/* Tabs */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 24, borderBottom: '1px solid #1e2535', paddingBottom: 0 }}>
        {([
          { id: 'org',     label: 'Organization', icon: Shield },
          { id: 'devices', label: 'Devices',       icon: Terminal },
          { id: 'account', label: 'Account',        icon: User },
        ] as const).map(({ id, label, icon: Icon }) => (
          <button
            key={id}
            onClick={() => setActiveTab(id)}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              padding: '8px 16px', fontSize: 13, cursor: 'pointer',
              background: 'none', border: 'none',
              color: activeTab === id ? '#60a5fa' : '#64748b',
              borderBottom: `2px solid ${activeTab === id ? '#2563eb' : 'transparent'}`,
              marginBottom: -1,
              fontWeight: activeTab === id ? 500 : 400,
            }}
          >
            <Icon size={14} />
            {label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {activeTab === 'org' && <OrgTab org={orgData} />}
      {activeTab === 'devices' && <DevicesTab token={session?.accessToken} />}
      {activeTab === 'account' && <AccountTab session={session} />}
    </div>
  )
}

// ─── Org tab ──────────────────────────────────────────────────────────────────

function OrgTab({ org }: { org: any }) {
  return (
    <div style={{ maxWidth: 540 }}>
      <Section title="Organization details">
        <InfoRow label="Name" value={org?.name ?? '—'} />
        <InfoRow label="Slug" value={org?.slug ?? '—'} mono />
        <InfoRow label="Plan" value={org?.plan ?? '—'} />
        <InfoRow label="Org ID" value={org?.id ?? '—'} mono copyable />
      </Section>
    </div>
  )
}

// ─── Devices tab ─────────────────────────────────────────────────────────────

function DevicesTab({ token }: { token?: string }) {
  const [provToken, setProvToken] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const generateToken = async () => {
    setLoading(true)
    try {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL
      const resp = await fetch(`${apiUrl}/api/provision-tokens`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
      })
      const data = await resp.json()
      setProvToken(data.token)
    } finally {
      setLoading(false)
    }
  }

  const installCmd = provToken
    ? `curl -fsSL https://${process.env.NEXT_PUBLIC_API_URL?.replace('https://', '')?.replace('/api', '')}/install | sudo bash -s ${provToken}`
    : null

  return (
    <div style={{ maxWidth: 640 }}>
      <Section title="Add a new device">
        <p style={{ color: '#94a3b8', fontSize: 13, lineHeight: 1.6, margin: '0 0 16px' }}>
          Generate a one-time provisioning token and run the install command on the
          mini-PC connected to your camera network. The device will register automatically
          and cameras will appear in your dashboard within 60 seconds.
        </p>

        <button
          onClick={generateToken}
          disabled={loading}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '8px 18px', borderRadius: 8, fontSize: 13, fontWeight: 500,
            background: loading ? '#1e3a5f' : '#2563eb', border: 'none',
            color: 'white', cursor: loading ? 'not-allowed' : 'pointer',
          }}
        >
          <RefreshCw size={13} style={{ animation: loading ? 'spin 0.8s linear infinite' : 'none' }} />
          {loading ? 'Generating...' : 'Generate install token'}
        </button>
        <style>{`@keyframes spin { to { transform: rotate(360deg) } }`}</style>

        {provToken && installCmd && (
          <div style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: '#64748b', marginBottom: 6 }}>
              Run this command on the mini-PC (valid for 24 hours):
            </div>
            <CodeBlock code={installCmd} />
            <div style={{ fontSize: 11, color: '#475569', marginTop: 8 }}>
              Requirements: Ubuntu 22.04+, 4+ cores, 8GB RAM, network access to cameras.
            </div>
          </div>
        )}
      </Section>

      <Section title="What the installer does">
        <ol style={{ color: '#94a3b8', fontSize: 13, lineHeight: 2, paddingLeft: 18, margin: 0 }}>
          <li>Installs Docker, WireGuard, and ffmpeg</li>
          <li>Exchanges your token for a device key + WireGuard credentials</li>
          <li>Configures a secure WireGuard tunnel back to this control plane</li>
          <li>Starts Frigate NVR and a local Mosquitto MQTT broker</li>
          <li>Runs the cam-agent which scans for ONVIF cameras and registers them</li>
          <li>Installs everything as systemd services that auto-start on reboot</li>
        </ol>
      </Section>
    </div>
  )
}

// ─── Account tab ──────────────────────────────────────────────────────────────

function AccountTab({ session }: { session: any }) {
  return (
    <div style={{ maxWidth: 480 }}>
      <Section title="Account details">
        <InfoRow label="Email" value={session?.user?.email ?? '—'} />
        <InfoRow label="Name"  value={session?.user?.name  ?? '—'} />
        <InfoRow label="Roles" value={session?.roles?.join(', ') ?? '—'} />
      </Section>

      <Section title="Password">
        <p style={{ color: '#94a3b8', fontSize: 13, margin: '0 0 12px' }}>
          Passwords are managed through Keycloak.
        </p>
        <a
          href={`${process.env.NEXT_PUBLIC_KC_URL}/realms/${process.env.NEXT_PUBLIC_KC_REALM}/account`}
          target="_blank"
          rel="noreferrer"
          style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '7px 16px', borderRadius: 7, fontSize: 13,
            background: '#1e2535', color: '#94a3b8',
            textDecoration: 'none', border: '1px solid #1e2535',
          }}
        >
          Manage account in Keycloak →
        </a>
      </Section>
    </div>
  )
}

// ─── Shared UI components ─────────────────────────────────────────────────────

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 28 }}>
      <div style={{
        fontSize: 12, fontWeight: 500, color: '#64748b',
        textTransform: 'uppercase', letterSpacing: '0.06em',
        marginBottom: 12, paddingBottom: 8,
        borderBottom: '1px solid #1e2535',
      }}>
        {title}
      </div>
      {children}
    </div>
  )
}

function InfoRow({ label, value, mono, copyable }: {
  label: string
  value: string
  mono?: boolean
  copyable?: boolean
}) {
  const [copied, setCopied] = useState(false)

  const copy = () => {
    navigator.clipboard.writeText(value)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div style={{
      display: 'flex', justifyContent: 'space-between', alignItems: 'center',
      padding: '9px 0', borderBottom: '1px solid #1e2535',
    }}>
      <span style={{ fontSize: 13, color: '#64748b', minWidth: 120 }}>{label}</span>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{
          fontSize: 13, color: '#e2e8f0',
          fontFamily: mono ? 'monospace' : 'inherit',
        }}>
          {value}
        </span>
        {copyable && (
          <button
            onClick={copy}
            style={{
              padding: 4, borderRadius: 4, background: '#1e2535',
              border: 'none', cursor: 'pointer',
              color: copied ? '#4ade80' : '#64748b',
              display: 'flex', alignItems: 'center',
            }}
          >
            {copied ? <Check size={12} /> : <Copy size={12} />}
          </button>
        )}
      </div>
    </div>
  )
}

function CodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false)

  const copy = () => {
    navigator.clipboard.writeText(code)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div style={{ position: 'relative' }}>
      <pre style={{
        background: '#0a0e1a', border: '1px solid #1e2535', borderRadius: 8,
        padding: '12px 40px 12px 14px', fontSize: 12,
        fontFamily: 'monospace', color: '#94a3b8', overflowX: 'auto',
        whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0,
      }}>
        {code}
      </pre>
      <button
        onClick={copy}
        style={{
          position: 'absolute', top: 8, right: 8,
          padding: 5, borderRadius: 5, background: '#1e2535',
          border: 'none', cursor: 'pointer',
          color: copied ? '#4ade80' : '#64748b',
          display: 'flex', alignItems: 'center',
        }}
      >
        {copied ? <Check size={13} /> : <Copy size={13} />}
      </button>
    </div>
  )
}
