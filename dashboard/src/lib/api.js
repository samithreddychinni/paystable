import { mockApi } from './mockData'

const BASE = '/api/v1/admin'
const USE_MOCKS = false // Set to false when Go server is running

async function get(path, params = {}) {
  if (USE_MOCKS) {
    await new Promise(r => setTimeout(r, 200 + Math.random() * 300)) // Simulate latency

    // Handle parameterized routes like /transactions/:id
    const paramMatch = path.match(/^\/transactions\/(.+)$/)
    if (paramMatch) {
      return mockApi['/transactions/:id'](paramMatch[1])
    }

    const handler = mockApi[path]
    if (handler) return handler(params)

    // Try with query params stripped
    const basePath = path.split('?')[0]
    if (mockApi[basePath]) return mockApi[basePath](params)

    console.warn(`No mock handler for: ${path}`)
    return {}
  }

  const url = new URL(BASE + path, window.location.origin)
  Object.entries(params).forEach(([k, v]) => v != null && url.searchParams.set(k, String(v)))
  const res = await fetch(url.toString())
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

async function post(path, body = {}) {
  if (USE_MOCKS) {
    await new Promise(r => setTimeout(r, 300 + Math.random() * 500))
    
    const replayMatch = path.match(/^\/deliveries\/(.+)\/replay$/)
    if (replayMatch) return mockApi['/deliveries/:id/replay']()
    if (path === '/config/rotate-secret') return mockApi['/config/rotate-secret']()

    console.warn(`No mock handler for POST: ${path}`)
    return { success: true }
  }

  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export const api = {
  // Overview
  getOverviewStats:     ()      => get('/overview/stats'),
  getLedgerFeed:        ()      => get('/ledger/recent'),
  getVolumeChart:       ()      => get('/overview/volume'),
  getMismatchRateChart: ()      => get('/overview/mismatch-rate'),

  // Transactions
  getTransactions:      (p)     => get('/transactions', p),
  getTransaction:       (id)    => get(`/transactions/${id}`),

  // Mismatches
  getMismatches:        (p)     => get('/mismatches', p),
  getMismatchStats:     ()      => get('/mismatches/stats'),
  getReviews:           ()      => get('/reviews'),
  resolveReview:        (id, p) => post(`/reviews/${id}`, p),

  // Delivery
  getDeliveries:        (p)     => get('/deliveries', p),
  getDeliveryStats:     ()      => get('/deliveries/stats'),
  replayDelivery:       (id)    => post(`/deliveries/${id}/replay`),

  // Config
  getConfig:            ()      => get('/config'),
  getRotationStatus:    ()      => get('/config/rotation-status'),
  rotateSecret:         (body)  => post('/config/rotate-secret', body),
}
