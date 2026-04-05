'use client'
import { SessionProvider, useSession, signOut } from 'next-auth/react'
import { usePathname, useRouter } from 'next/navigation'
import Link from 'next/link'
import { useEffect } from 'react'
import {
  Video, MapPin, Bell, Settings, LogOut, Wifi, WifiOff, Activity
} from 'lucide-react'
import clsx from 'clsx'

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <title>Cam Platform</title>
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <link rel="icon" href="/favicon.ico" />
      </head>
      <body style={{ margin: 0, background: '#0f1117', color: '#e2e8f0', fontFamily: 'system-ui, sans-serif' }}>
        <SessionProvider>
          <AppShell>{children}</AppShell>
        </SessionProvider>
      </body>
    </html>
  )
}

function AppShell({ children }: { children: React.ReactNode }) {
  const { data: session, status } = useSession()
  const pathname = usePathname()
  const router = useRouter()

  // Redirect to login if not authenticated
  useEffect(() => {
    if (status === 'unauthenticated' && !pathname.startsWith('/auth')) {
      router.push('/api/auth/signin')
    }
  }, [status, pathname, router])

  if (status === 'loading') return <LoadingScreen />
  if (status === 'unauthenticated') return null
  if (pathname.startsWith('/auth')) return <>{children}</>

  const nav = [
    { href: '/cameras', icon: Video,    label: 'Cameras' },
    { href: '/events',  icon: Activity, label: 'Events' },
    { href: '/sites',   icon: MapPin,   label: 'Sites' },
    { href: '/alerts',  icon: Bell,     label: 'Alerts' },
    { href: '/settings',icon: Settings, label: 'Settings' },
  ]

  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      {/* Sidebar */}
      <aside style={{
        width: 220, flexShrink: 0,
        background: '#161b27',
        borderRight: '1px solid #1e2535',
        display: 'flex', flexDirection: 'column',
        position: 'sticky', top: 0, height: '100vh',
      }}>
        {/* Logo */}
        <div style={{ padding: '20px 20px 16px', borderBottom: '1px solid #1e2535' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <div style={{
              width: 28, height: 28, borderRadius: 6,
              background: 'linear-gradient(135deg, #3b82f6, #6366f1)',
              display: 'flex', alignItems: 'center', justifyContent: 'center'
            }}>
              <Video size={14} color="white" />
            </div>
            <span style={{ fontWeight: 600, fontSize: 15, color: '#f1f5f9' }}>
              Cam Platform
            </span>
          </div>
          <div style={{ marginTop: 6, fontSize: 11, color: '#64748b' }}>
            {session?.user?.email}
          </div>
        </div>

        {/* Nav */}
        <nav style={{ flex: 1, padding: '8px 10px' }}>
          {nav.map(({ href, icon: Icon, label }) => {
            const active = pathname.startsWith(href)
            return (
              <Link key={href} href={href} style={{ textDecoration: 'none' }}>
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 10,
                  padding: '8px 12px', borderRadius: 8, marginBottom: 2,
                  background: active ? '#1e3a5f' : 'transparent',
                  color: active ? '#60a5fa' : '#94a3b8',
                  fontSize: 14, fontWeight: active ? 500 : 400,
                  transition: 'all .15s',
                  cursor: 'pointer',
                }}>
                  <Icon size={16} />
                  {label}
                </div>
              </Link>
            )
          })}
        </nav>

        {/* Footer */}
        <div style={{ padding: '12px 10px', borderTop: '1px solid #1e2535' }}>
          <button
            onClick={() => signOut({ callbackUrl: '/api/auth/signin' })}
            style={{
              display: 'flex', alignItems: 'center', gap: 10,
              padding: '8px 12px', borderRadius: 8, width: '100%',
              background: 'none', border: 'none', cursor: 'pointer',
              color: '#64748b', fontSize: 14,
            }}
          >
            <LogOut size={15} />
            Sign out
          </button>
        </div>
      </aside>

      {/* Main */}
      <main style={{ flex: 1, minWidth: 0, overflowY: 'auto' }}>
        {session?.error === 'RefreshAccessTokenError' && (
          <div style={{
            background: '#7f1d1d', color: '#fca5a5',
            padding: '10px 20px', fontSize: 13,
          }}>
            Your session expired.{' '}
            <button
              onClick={() => signOut()}
              style={{ color: '#fca5a5', background: 'none', border: 'none', cursor: 'pointer', textDecoration: 'underline' }}
            >
              Sign in again
            </button>
          </div>
        )}
        {children}
      </main>
    </div>
  )
}

function LoadingScreen() {
  return (
    <div style={{
      minHeight: '100vh', display: 'flex', alignItems: 'center',
      justifyContent: 'center', background: '#0f1117',
    }}>
      <div style={{ color: '#64748b', fontSize: 14 }}>Loading...</div>
    </div>
  )
}
