/**
 * Dashboard tests — uses Vitest + React Testing Library.
 *
 * Install:
 *   npm install -D vitest @vitejs/plugin-react @testing-library/react
 *               @testing-library/user-event @testing-library/jest-dom
 *               jsdom msw
 */

// ─── CameraPlayer tests ───────────────────────────────────────────────────────

import { describe, it, expect, vi, beforeEach, beforeAll, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'
import React from 'react'

// Mock hls.js (not available in jsdom)
vi.mock('hls.js', () => ({
  default: class MockHls {
    static isSupported() { return true }
    static Events = {
      MANIFEST_PARSED: 'hlsManifestParsed',
      MEDIA_ATTACHED: 'hlsMediaAttached',
      ERROR: 'hlsError',
    }
    loadSource = vi.fn()
    attachMedia = vi.fn()
    destroy = vi.fn()
    on = vi.fn((event: string, cb: Function) => {
      if (event === 'hlsMediaAttached') {
        setTimeout(() => cb(), 0)
      }
    })
  }
}))

// Mock next-auth
vi.mock('next-auth/react', () => ({
  useSession: () => ({ data: { accessToken: 'test-token', orgId: 'org-1' }, status: 'authenticated' }),
  getSession: () => Promise.resolve({ accessToken: 'test-token', orgId: 'org-1' }),
  signIn: vi.fn(),
  signOut: vi.fn(),
  SessionProvider: ({ children }: any) => children,
}))

// ─── CameraPlayer ─────────────────────────────────────────────────────────────

describe('CameraPlayer', () => {
  let CameraPlayer: any

  beforeAll(async () => {
    const mod = await import('../components/camera/CameraPlayer')
    CameraPlayer = mod.CameraPlayer
  })

  const defaultProps = {
    cameraId: 'cam-001',
    streamUrl: 'http://localhost/hls/live.m3u8',
    snapshotUrl: 'http://localhost/snapshot.jpg',
    name: 'Front Door',
    status: 'online' as const,
  }

  it('renders camera name label', () => {
    render(<CameraPlayer {...defaultProps} />)
    expect(screen.getByText('Front Door')).toBeInTheDocument()
  })

  it('shows LIVE badge when playing', async () => {
    render(<CameraPlayer {...defaultProps} />)
    // After HLS attaches, state becomes 'playing'
    await waitFor(() => {
      expect(screen.queryByText('LIVE')).toBeInTheDocument()
    }, { timeout: 500 })
  })

  it('shows offline state when status is offline', () => {
    render(<CameraPlayer {...defaultProps} status="offline" />)
    expect(screen.getByText('Camera offline')).toBeInTheDocument()
  })

  it('does not show LIVE badge when offline', () => {
    render(<CameraPlayer {...defaultProps} status="offline" />)
    expect(screen.queryByText('LIVE')).not.toBeInTheDocument()
  })

  it('calls onClick when clicked', async () => {
    const onClick = vi.fn()
    render(<CameraPlayer {...defaultProps} onClick={onClick} />)
    const container = screen.getByText('Front Door').closest('div[style]')
    if (container) {
      await userEvent.click(container)
      expect(onClick).toHaveBeenCalled()
    }
  })
})

// ─── API client tests ─────────────────────────────────────────────────────────

describe('API client', () => {
  let cameras: any
  let sites: any
  let events: any

  // Mock fetch
  const mockFetch = vi.fn()

  beforeAll(async () => {
    global.fetch = mockFetch
    const mod = await import('../lib/api')
    cameras = mod.cameras
    sites = mod.sites
    events = mod.events
  })

  beforeEach(() => {
    mockFetch.mockReset()
    global.fetch = mockFetch
  })

  it('cameras.list includes Authorization header', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify([]), { status: 200 }))
    await cameras.list()
    const [, options] = mockFetch.mock.calls[0]
    expect((options as RequestInit).headers).toMatchObject({
      Authorization: 'Bearer test-token',
    })
  })

  it('cameras.list with siteId passes query param', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify([]), { status: 200 }))
    await cameras.list('site-abc')
    const [url] = mockFetch.mock.calls[0]
    expect(url).toContain('site_id=site-abc')
  })

  it('cameras.streamUrl returns correct URL', () => {
    const url = cameras.streamUrl('cam-001')
    expect(url).toContain('cam-001')
    expect(url).toContain('hls')
    expect(url).toContain('live.m3u8')
  })

  it('sites.list returns array', async () => {
    const fakeSites = [{ id: 's1', name: 'Site 1', address: '', timezone: 'UTC' }]
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(fakeSites), { status: 200 }))
    const result = await sites.list()
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('Site 1')
  })

  it('throws on non-OK non-401 responses', async () => {
    mockFetch.mockResolvedValueOnce(new Response('Server error', { status: 500 }))
    await expect(cameras.list()).rejects.toThrow('API error 500')
  })
})

// ─── connectEventStream tests ─────────────────────────────────────────────────

describe('connectEventStream', () => {
  let connectEventStream: any

  beforeAll(async () => {
    const mod = await import('../lib/api')
    connectEventStream = mod.connectEventStream
  })

  it('calls onEvent when WebSocket message received', async () => {
    const onEvent = vi.fn()
    const onStatus = vi.fn()

    // Mock WebSocket
    const mockWS = {
      onopen: null as any,
      onmessage: null as any,
      onclose: null as any,
      onerror: null as any,
      close: vi.fn(),
      readyState: 1,
    }
    global.WebSocket = vi.fn(() => mockWS) as any

    const disconnect = connectEventStream('test-token', onEvent, onStatus)

    // Simulate connection open
    mockWS.onopen?.({} as Event)
    expect(onStatus).toHaveBeenCalledWith(true)

    // Simulate incoming event
    const event = {
      type: 'event',
      event_id: 'ev-001',
      camera_id: 'cam-001',
      event_type: 'person',
      payload: { after: { label: 'person', score: 0.95 } },
      timestamp: new Date().toISOString(),
    }
    mockWS.onmessage?.({ data: JSON.stringify(event) } as MessageEvent)
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ event_id: 'ev-001' }))

    // Cleanup
    disconnect()
    expect(mockWS.close).toHaveBeenCalled()
  })

  it('reconnects on disconnect with backoff', async () => {
    vi.useFakeTimers()
    let wsInstances: any[] = []
    global.WebSocket = vi.fn(() => {
      const ws = { onopen: null, onmessage: null, onclose: null, onerror: null, close: vi.fn() }
      wsInstances.push(ws)
      return ws
    }) as any

    connectEventStream('token', vi.fn(), vi.fn())

    expect(wsInstances).toHaveLength(1)

    // Simulate disconnect
    wsInstances[0].onclose?.()

    // Advance past retry delay
    await vi.advanceTimersByTimeAsync(1100)
    expect(wsInstances).toHaveLength(2)

    vi.useRealTimers()
  })
})
