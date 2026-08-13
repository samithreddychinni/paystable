import { useState, useEffect } from 'react'
import { api } from '../../lib/api'
import { cn, truncate } from '../../lib/utils'

const GATEWAY_DOT = {
  PayU:     'bg-status-yellow',
  Razorpay: 'bg-status-blue',
  Cashfree: 'bg-status-cyan',
  PhonePe:  'bg-status-purple',
}

const DELIVERY_LABEL = {
  pending: 'Queued',
  in_flight: 'Sending',
  delivered: 'Delivered',
  exhausted: 'Needs replay',
}

export default function Mismatches() {
  const [reviews, setReviews]       = useState([])
  const [loading, setLoading]       = useState(true)
  const [error, setError]           = useState(null)

  useEffect(() => {
    async function load() {
      try {
        const result = await api.getReviews()
        setReviews(result.data)
      } catch (err) {
        setError(err.message)
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [])

  const refresh = async () => {
    const result = await api.getReviews()
    setReviews(result.data)
  }

  const resolve = async (txnID) => {
    const resolution = window.prompt('Resolution: confirmed, failed, or no_action', 'no_action')
    if (!resolution) return
    const operator = window.prompt('Your name')
    if (!operator) return
    const note = window.prompt('Why was this resolved?')
    if (!note) return
    try {
      await api.resolveReview(txnID, { resolution, operator, note })
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  const replay = async (deliveryID) => {
    try {
      await api.replayDelivery(deliveryID)
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  if (loading) {
    return (
      <div className="space-y-4">
        <div className="h-16 bg-bg-surface rounded-xl animate-pulse" />
        <div className="h-64 bg-bg-surface rounded-xl animate-pulse" />
      </div>
    )
  }

  if (error) {
    return <div className="text-sm text-status-red p-4">{error}</div>
  }

  return (
    <div className="space-y-5">

      <div className="flex items-end justify-between">
        <div>
          <p className="text-3xl font-mono font-medium text-text-primary">{reviews.filter(review => !review.resolution).length}</p>
          <p className="text-sm text-text-muted mt-0.5">
            transactions need an operator decision
          </p>
        </div>
      </div>

      {reviews.length === 0 ? (
        <div className="bg-bg-surface border border-bg-border rounded-xl py-16 text-center">
          <p className="text-sm text-text-muted">Nothing needs review.</p>
          <p className="text-xs text-text-muted mt-1 max-w-sm mx-auto">
            Mismatches and inconclusive verification results will appear here.
          </p>
        </div>
      ) : (
        <div className="bg-bg-surface border border-bg-border rounded-xl overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-bg-border">
                {['TXN ID', 'Gateway', 'Status', 'Resolution', 'Delivery', 'Action'].map(h => (
                  <th key={h} className="text-left text-xs text-text-muted px-4 py-2.5 font-normal">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {reviews.map((review) => (
                <tr key={review.txn_id} className="border-b border-bg-border last:border-0 hover:bg-bg-elevated transition-colors">
                  <td className="px-4 py-3 text-xs font-mono text-text-primary">{truncate(review.txn_id, 16)}</td>
                  <td className="px-4 py-3">
                    <span className="flex items-center gap-1.5 text-xs text-text-secondary">
                      <span className={cn('h-1.5 w-1.5 rounded-full', GATEWAY_DOT[review.gateway] || 'bg-text-muted')} />
                      {review.gateway}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-xs font-mono text-status-yellow">{review.status}</td>
                  <td className="px-4 py-3 text-xs text-text-secondary">{review.resolution || 'Awaiting review'}</td>
                  <td className={cn('px-4 py-3 text-xs', review.delivery_state === 'exhausted' ? 'text-status-red' : 'text-text-muted')}>
                    {review.resolution === 'no_action' ? 'Record only' : DELIVERY_LABEL[review.delivery_state] || 'Not queued'}
                  </td>
                  <td className="px-4 py-3">
                    {!review.resolution && (
                      <button onClick={() => resolve(review.txn_id)} className="text-xs text-text-secondary hover:text-text-primary">
                        Resolve
                      </button>
                    )}
                    {review.delivery_state === 'exhausted' && (
                      <button onClick={() => replay(review.delivery_id)} className="text-xs text-status-red hover:text-text-primary">
                        Replay
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

    </div>
  )
}
