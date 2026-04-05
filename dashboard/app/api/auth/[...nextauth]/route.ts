import NextAuth, { NextAuthOptions } from 'next-auth'
import KeycloakProvider from 'next-auth/providers/keycloak'

declare module 'next-auth' {
  interface Session {
    accessToken: string
    orgId: string
    roles: string[]
    error?: string
  }
  interface Token {
    accessToken: string
    refreshToken: string
    expiresAt: number
    orgId: string
    roles: string[]
    error?: string
  }
}

const authOptions: NextAuthOptions = {
  providers: [
    KeycloakProvider({
      clientId: process.env.KEYCLOAK_CLIENT_ID!,
      clientSecret: process.env.KEYCLOAK_CLIENT_SECRET!,
      issuer: process.env.KEYCLOAK_ISSUER!,
      // The realm attaches org-claims as a default client scope.
      // Requesting it explicitly can trigger invalid_scope in local Keycloak.
      authorization: { params: { scope: 'openid email profile' } },
    }),
  ],

  callbacks: {
    // Persist access token + custom claims into the JWT
    async jwt({ token, account, profile }) {
      if (account) {
        // Initial sign in — store tokens
        token.accessToken = account.access_token as string
        token.refreshToken = account.refresh_token as string
        token.expiresAt = (account.expires_at as number) * 1000

        // Extract our custom claims from the access token payload
        const payload = parseJWTPayload(token.accessToken)
        token.orgId = payload.org_id ?? ''
        token.roles = payload.roles ?? []
      }

      // Token still valid
      if (Date.now() < (token.expiresAt as number) - 30_000) {
        return token
      }

      // Access token expired — refresh it
      return refreshAccessToken(token as any)
    },

    // Expose what the client needs from the session
    async session({ session, token }) {
      session.accessToken = token.accessToken as string
      session.orgId = token.orgId as string
      session.roles = (token.roles as string[]) ?? []
      if (token.error) session.error = token.error as string
      return session
    },
  },

  pages: {
    signIn: '/auth/signin',
    error: '/auth/error',
  },

  session: { strategy: 'jwt' },
}

const handler = NextAuth(authOptions)
export { handler as GET, handler as POST }

// ─── Helpers ─────────────────────────────────────────────────────────────────

function parseJWTPayload(token: string): Record<string, any> {
  try {
    const payload = token.split('.')[1]
    const decoded = Buffer.from(payload, 'base64url').toString('utf-8')
    return JSON.parse(decoded)
  } catch {
    return {}
  }
}

async function refreshAccessToken(token: {
  refreshToken: string
  expiresAt: number
  orgId: string
  roles: string[]
}) {
  try {
    const url = `${process.env.KEYCLOAK_ISSUER}/protocol/openid-connect/token`
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        client_id: process.env.KEYCLOAK_CLIENT_ID!,
        client_secret: process.env.KEYCLOAK_CLIENT_SECRET!,
        grant_type: 'refresh_token',
        refresh_token: token.refreshToken,
      }),
    })

    const data = await resp.json()
    if (!resp.ok) throw data

    const payload = parseJWTPayload(data.access_token)
    return {
      ...token,
      accessToken: data.access_token,
      refreshToken: data.refresh_token ?? token.refreshToken,
      expiresAt: Date.now() + data.expires_in * 1000,
      orgId: payload.org_id ?? token.orgId,
      roles: payload.roles ?? token.roles,
    }
  } catch (err) {
    console.error('[auth] refresh failed:', err)
    return { ...token, error: 'RefreshAccessTokenError' }
  }
}
