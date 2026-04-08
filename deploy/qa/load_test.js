import http from 'k6/http'
import { check, sleep } from 'k6'

export const options = {
  vus: 20,
  duration: '1m',
}

const API_BASE = __ENV.API_BASE || ''

export default function () {
  const res = http.get(`${API_BASE}/health`)
  check(res, { 'health 200': (r) => r.status === 200 })
  sleep(1)
}
