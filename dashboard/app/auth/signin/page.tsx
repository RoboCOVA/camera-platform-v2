'use client'
import { signIn } from 'next-auth/react'
import { useSearchParams } from 'next/navigation'
import { Video } from 'lucide-react'

export default function SignInPage() {
  const params = useSearchParams()
  const error = params.get('error')
  const callbackUrl = params.get('callbackUrl') ?? '/cameras'

  return (
    <div style={{
      minHeight: '100vh', display: 'flex', alignItems: 'center',
      justifyContent: 'center', background: '#0f1117',
      fontFamily: 'system-ui, sans-serif',
    }}>
      <div style={{
        width: '100%', maxWidth: 380,
        background: '#161b27', border: '1px solid #1e2535',
        borderRadius: 14, padding: '36px 32px',
        textAlign: 'center',
      }}>
        {/* Logo */}
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 20 }}>
          <div style={{
            width: 48, height: 48, borderRadius: 12,
            background: 'linear-gradient(135deg, #3b82f6, #6366f1)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <Video size={22} color="white" />
          </div>
        </div>

        <h1 style={{
          margin: '0 0 6px', fontSize: 20, fontWeight: 600, color: '#f1f5f9',
        }}>
          Cam Platform
        </h1>
        <p style={{ margin: '0 0 28px', fontSize: 13, color: '#64748b' }}>
          Sign in to manage your cameras
        </p>

        {/* Error */}
        {error && (
          <div style={{
            background: '#7f1d1d', border: '1px solid #991b1b',
            borderRadius: 8, padding: '10px 14px', marginBottom: 18,
            fontSize: 13, color: '#fca5a5', textAlign: 'left',
          }}>
            {error === 'OAuthSignin' ? 'Could not connect to authentication server.' :
             error === 'AccessDenied' ? 'Your account does not have access.' :
             'Sign-in failed. Please try again.'}
          </div>
        )}

        {/* Sign in button */}
        <button
          onClick={() => signIn('keycloak', { callbackUrl })}
          style={{
            width: '100%', padding: '11px 0', borderRadius: 8,
            background: '#2563eb', border: 'none', cursor: 'pointer',
            color: 'white', fontSize: 14, fontWeight: 500,
            transition: 'background .15s',
          }}
          onMouseEnter={e => (e.currentTarget.style.background = '#1d4ed8')}
          onMouseLeave={e => (e.currentTarget.style.background = '#2563eb')}
        >
          Sign in with SSO
        </button>

        <p style={{ margin: '20px 0 0', fontSize: 11, color: '#475569', lineHeight: 1.6 }}>
          Secured by Keycloak. Supports Google, Microsoft,<br />
          and LDAP/Active Directory federation.
        </p>
      </div>
    </div>
  )
}
