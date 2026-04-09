import http from 'k6/http'
import { check, sleep } from 'k6'

export const options = {
  vus: 20,
  duration: '1m',
}

const API_BASE = __ENV.API_BASE || ''
const API_TOKEN = __ENV.API_TOKEN || ''

export default function () {
  const params = API_TOKEN ? { headers: { Authorization: `Bearer ${API_TOKEN}` } } : {}
  const camerasRes = http.get(`${API_BASE}/api/cameras?limit=20`, params)
  check(camerasRes, { 'cameras 200': (r) => r.status === 200 })

  const eventsRes = http.get(`${API_BASE}/api/events?limit=20`, params)
  check(eventsRes, { 'events 200': (r) => r.status === 200 })
  sleep(1)
}
