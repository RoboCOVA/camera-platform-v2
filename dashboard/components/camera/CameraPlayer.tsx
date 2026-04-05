'use client'
import { useEffect, useRef, useState, useCallback } from 'react'
import type Hls from 'hls.js'

interface CameraPlayerProps {
  cameraId: string
  streamUrl: string     // HLS .m3u8 URL
  snapshotUrl: string   // fallback JPEG
  name: string
  status: 'online' | 'offline' | 'error'
  width?: number
  height?: number
  showLabel?: boolean
  onClick?: () => void
}

type PlayerState = 'loading' | 'playing' | 'error' | 'offline'

export function CameraPlayer({
  cameraId,
  streamUrl,
  snapshotUrl,
  name,
  status,
  showLabel = true,
  onClick,
}: CameraPlayerProps) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const hlsRef = useRef<Hls | null>(null)
  const [playerState, setPlayerState] = useState<PlayerState>(
    status === 'offline' ? 'offline' : 'loading'
  )
  const [retryCount, setRetryCount] = useState(0)

  const initPlayer = useCallback(async () => {
    if (!videoRef.current || status === 'offline') return

    // Dynamically import hls.js — avoid SSR issues
    const HlsLib = (await import('hls.js')).default

    // Clean up previous instance
    if (hlsRef.current) {
      hlsRef.current.destroy()
      hlsRef.current = null
    }

    const video = videoRef.current
    setPlayerState('loading')

    if (HlsLib.isSupported()) {
      const hls = new HlsLib({
        lowLatencyMode: true,
        backBufferLength: 30,
        maxBufferLength: 10,
        maxMaxBufferLength: 30,
        liveSyncDurationCount: 3,
        enableWorker: true,
        // Retry settings for flaky on-prem connections
        manifestLoadingTimeOut: 10000,
        manifestLoadingMaxRetry: 3,
        levelLoadingTimeOut: 10000,
        fragLoadingTimeOut: 20000,
      })

      hls.loadSource(streamUrl)
      hls.attachMedia(video)

      hls.on(HlsLib.Events.MANIFEST_PARSED, () => {
        video.play().catch(() => {
          // Autoplay blocked — show play button (handled by playerState)
        })
      })

      hls.on(HlsLib.Events.MEDIA_ATTACHED, () => {
        setPlayerState('playing')
      })

      hls.on(HlsLib.Events.ERROR, (_, data) => {
        if (data.fatal) {
          setPlayerState('error')
          hls.destroy()
          // Auto-retry after 5s for fatal errors
          setTimeout(() => setRetryCount(c => c + 1), 5000)
        }
      })

      if (video) {
        video.onplaying = () => setPlayerState('playing')
      }
      hlsRef.current = hls

    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      // Safari native HLS
      video.src = streamUrl
      video.onloadedmetadata = () => video.play().catch(() => {})
      video.onplaying = () => setPlayerState('playing')
      video.onerror = () => setPlayerState('error')
    } else {
      setPlayerState('error')
    }
  }, [streamUrl, status, retryCount])

  useEffect(() => {
    initPlayer()
    return () => {
      hlsRef.current?.destroy()
      hlsRef.current = null
    }
  }, [initPlayer])

  // When camera goes offline, tear down player
  useEffect(() => {
    if (status === 'offline') {
      hlsRef.current?.destroy()
      hlsRef.current = null
      setPlayerState('offline')
    }
  }, [status])

  const aspectRatio = '16/9'
  const isClickable = !!onClick

  return (
    <div
      onClick={onClick}
      style={{
        position: 'relative',
        background: '#0a0e1a',
        borderRadius: 10,
        overflow: 'hidden',
        aspectRatio,
        cursor: isClickable ? 'pointer' : 'default',
        border: '1px solid #1e2535',
      }}
    >
      {/* Video element */}
      <video
        ref={videoRef}
        autoPlay
        muted
        playsInline
        style={{
          width: '100%',
          height: '100%',
          objectFit: 'cover',
          display: playerState === 'playing' ? 'block' : 'none',
        }}
      />

      {/* Snapshot fallback while loading */}
      {playerState === 'loading' && snapshotUrl && (
        <img
          src={snapshotUrl}
          alt={name}
          style={{ width: '100%', height: '100%', objectFit: 'cover', opacity: 0.5 }}
        />
      )}

      {/* State overlays */}
      {playerState !== 'playing' && (
        <div style={{
          position: 'absolute', inset: 0,
          display: 'flex', flexDirection: 'column',
          alignItems: 'center', justifyContent: 'center',
          background: playerState === 'loading' ? 'transparent' : 'rgba(0,0,0,0.7)',
          gap: 8,
        }}>
          {playerState === 'loading' && (
            <Spinner />
          )}
          {playerState === 'offline' && (
            <>
              <div style={{ width: 32, height: 32, borderRadius: '50%', background: '#374151', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <div style={{ width: 3, height: 16, background: '#6b7280', borderRadius: 2 }} />
              </div>
              <span style={{ color: '#9ca3af', fontSize: 12 }}>Camera offline</span>
            </>
          )}
          {playerState === 'error' && (
            <>
              <div style={{ color: '#ef4444', fontSize: 12 }}>Stream error</div>
              <button
                onClick={(e) => { e.stopPropagation(); setRetryCount(c => c + 1) }}
                style={{
                  padding: '4px 12px', borderRadius: 6, fontSize: 11,
                  background: '#1e3a5f', border: '1px solid #2563eb',
                  color: '#93c5fd', cursor: 'pointer',
                }}
              >
                Retry
              </button>
            </>
          )}
        </div>
      )}

      {/* Live badge */}
      {playerState === 'playing' && (
        <div style={{
          position: 'absolute', top: 8, left: 8,
          background: '#dc2626', borderRadius: 4,
          padding: '2px 6px', fontSize: 10, fontWeight: 600,
          color: 'white', letterSpacing: '0.05em',
          display: 'flex', alignItems: 'center', gap: 4,
        }}>
          <div style={{ width: 5, height: 5, borderRadius: '50%', background: 'white' }} />
          LIVE
        </div>
      )}

      {/* Camera name label */}
      {showLabel && (
        <div style={{
          position: 'absolute', bottom: 0, left: 0, right: 0,
          background: 'linear-gradient(transparent, rgba(0,0,0,0.8))',
          padding: '16px 10px 8px',
        }}>
          <div style={{ color: '#e2e8f0', fontSize: 12, fontWeight: 500 }}>{name}</div>
        </div>
      )}

      {/* Hover overlay for clickable tiles */}
      {isClickable && playerState === 'playing' && (
        <div style={{
          position: 'absolute', inset: 0,
          background: 'rgba(59, 130, 246, 0)',
          transition: 'background .15s',
        }}
          onMouseEnter={e => (e.currentTarget.style.background = 'rgba(59,130,246,0.08)')}
          onMouseLeave={e => (e.currentTarget.style.background = 'rgba(59,130,246,0)')}
        />
      )}
    </div>
  )
}

function Spinner() {
  return (
    <div style={{
      width: 24, height: 24, border: '2px solid #1e2535',
      borderTopColor: '#3b82f6', borderRadius: '50%',
      animation: 'spin 0.8s linear infinite',
    }}>
      <style>{`@keyframes spin { to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
