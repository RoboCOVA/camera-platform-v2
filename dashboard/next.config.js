/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  experimental: { serverActions: { allowedOrigins: ['*'] } },
  async rewrites() {
    return [
      {
        // Proxy /api/cam/* → backend API (avoids CORS in dev)
        source: '/api/cam/:path*',
        destination: `${process.env.NEXT_PUBLIC_API_URL}/:path*`,
      },
    ]
  },
}
module.exports = nextConfig
