import '@testing-library/jest-dom'
import { vi } from 'vitest'

// Mock Next.js router
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn() }),
  usePathname: () => '/cameras',
  useSearchParams: () => new URLSearchParams(),
  redirect: vi.fn(),
}))

// Mock Next.js Link
vi.mock('next/link', () => ({
  default: ({ children, href }: any) => <a href={href}>{children}</a>,
}))

// Silence console.error for known React warnings in tests
const originalError = console.error
beforeEach(() => {
  console.error = (...args: any[]) => {
    if (typeof args[0] === 'string' && args[0].includes('Warning:')) return
    originalError(...args)
  }
})
afterEach(() => {
  console.error = originalError
})
