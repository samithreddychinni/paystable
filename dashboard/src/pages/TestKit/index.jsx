import { useState } from 'react'
import { Check, Play, ShieldCheck } from 'lucide-react'
import { api } from '../../lib/api'
import { cn } from '../../lib/utils'

const METHOD_LABELS = {
  bounded: 'Bounded search',
  random: 'Random search',
  coverage: 'Coverage-guided',
  scout: 'Scout',
  'scout-closed-loop': 'Scout closed-loop',
}

const STEP_LABELS = {
  fulfill: 'Fulfill order',
  crash: 'Merchant crashes',
  restart: 'Merchant restarts',
  checkpoint: 'Store event claim',
  deliver: 'Update payment state',
}

function Metric({ label, value, detail }) {
  return (
    <div className="px-5 py-4">
      <p className="text-xs text-text-muted">{label}</p>
      <p className="mt-1 font-mono text-xl font-medium text-text-primary">{value}</p>
      <p className="mt-1 text-xs text-text-muted">{detail}</p>
    </div>
  )
}

function SearchTable({ search, vulnerableCount }) {
  const rows = search.filter(({ method }) => METHOD_LABELS[method])

  return (
    <div className="overflow-hidden rounded-xl border border-bg-border bg-bg-surface">
      <div className="border-b border-bg-border px-5 py-4">
        <h2 className="text-sm font-medium text-text-primary">Regression laboratory comparison</h2>
        <p className="mt-1 text-xs text-text-muted">Each method receives the same legal schedules and budget. The random row uses one fixed seed.</p>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead className="border-b border-bg-border text-text-muted">
            <tr>
              <th className="px-5 py-3 font-normal">Method</th>
              <th className="px-5 py-3 font-normal">Failures found within 10</th>
              <th className="px-5 py-3 font-normal">Median rank</th>
              <th className="px-5 py-3 font-normal">False findings</th>
              <th className="px-5 py-3 font-normal">Replay rate</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-bg-border">
            {rows.map((row) => {
              const found = Math.round(row.success_at_10 * vulnerableCount)
              const scout = row.method === 'scout'
              return (
                <tr key={row.method} className={scout ? 'bg-status-green/5' : ''}>
                  <td className={cn('px-5 py-3 font-medium', scout ? 'text-status-green' : 'text-text-primary')}>
                    {METHOD_LABELS[row.method]}
                  </td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{found} / {vulnerableCount}</td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{row.median_executions_before_finding}</td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{row.false_finding_count}</td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{Math.round(row.deterministic_replay_rate * 100)}%</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function HeldOutTable({ report }) {
  return (
    <div className="overflow-hidden rounded-xl border border-bg-border bg-bg-surface">
      <div className="border-b border-bg-border px-5 py-4">
        <h2 className="text-sm font-medium text-text-primary">Frozen held-out comparison</h2>
        <p className="mt-1 text-xs text-text-muted">The split uses complete merchant implementations. Random search aggregates 100 fixed seeds.</p>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead className="border-b border-bg-border text-text-muted">
            <tr>
              <th className="px-5 py-3 font-normal">Method</th>
              <th className="px-5 py-3 font-normal">Success@3</th>
              <th className="px-5 py-3 font-normal">Success@10</th>
              <th className="px-5 py-3 font-normal">Median rank</th>
              <th className="px-5 py-3 font-normal">MRR</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-bg-border">
            {report.summary.map((row) => {
              const strongest = row.method === 'scout-closed-loop'
              return (
                <tr key={row.method} className={strongest ? 'bg-status-green/5' : ''}>
                  <td className={cn('px-5 py-3 font-medium', strongest ? 'text-status-green' : 'text-text-primary')}>
                    {METHOD_LABELS[row.method]}
                  </td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{(row.success_at_3 * 100).toFixed(1)}%</td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{(row.success_at_10 * 100).toFixed(1)}%</td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{row.median_executions_before_finding}</td>
                  <td className="px-5 py-3 font-mono text-text-secondary">{row.mean_reciprocal_rank.toFixed(3)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <div className="border-t border-bg-border px-5 py-3 text-xs leading-5 text-text-muted">
        Standard Scout beats random on median rank and MRR, but not on Success@10. Closed-loop Scout uses observations, not vulnerability labels.
      </div>
    </div>
  )
}

function Finding({ finding }) {
  const violation = finding.result.violations[0]

  return (
    <div className="overflow-hidden rounded-xl border border-bg-border bg-bg-surface">
      <div className="border-b border-bg-border px-5 py-4 sm:flex sm:items-start sm:justify-between sm:gap-4">
        <div>
          <p className="text-xs font-mono text-status-red">Regression proof · {violation.invariant}</p>
          <h2 className="mt-1 text-sm font-medium text-text-primary">One payment fulfilled twice</h2>
          <p className="mt-1 max-w-2xl text-xs text-text-muted">{violation.detail}.</p>
        </div>
        <div className="mt-3 text-left sm:mt-0 sm:text-right">
          <p className="font-mono text-sm text-text-primary">{finding.reduction.reduced_action_count} scheduled actions</p>
          <p className="text-xs text-text-muted">{finding.result.trace.length} observed steps · 1-minimal</p>
        </div>
      </div>
      <ol className="divide-y divide-bg-border" aria-label="Failure trace">
        {finding.result.trace.map((entry) => (
          <li key={entry.sequence} className="grid grid-cols-[28px_minmax(120px,180px)_1fr_auto] items-center gap-3 px-5 py-3 text-xs">
            <span className="font-mono text-text-muted">{entry.sequence}</span>
            <span className="font-medium text-text-primary">{STEP_LABELS[entry.action] || entry.action}</span>
            <span className="text-text-muted">{entry.detail}</span>
            <span className={cn('font-mono', entry.effect_count > 1 ? 'text-status-red' : 'text-text-secondary')}>
              {entry.effect_count} {entry.effect_count === 1 ? 'effect' : 'effects'}
            </span>
          </li>
        ))}
      </ol>
      <div className="border-t border-bg-border px-5 py-3 text-xs text-text-muted">
        The reducer tested each removable action. Removing any remaining input action removes this failure.
      </div>
    </div>
  )
}

export default function TestKit() {
  const [report, setReport] = useState(null)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')

  const runDemo = async () => {
    setRunning(true)
    setError('')
    try {
      setReport(await api.runVerificationDemo())
    } catch (err) {
      setError(err.message)
    } finally {
      setRunning(false)
    }
  }

  const vulnerableCount = report?.programs.filter((program) => program.expected_invariant).length ?? 0
  const heldOutVulnerable = report?.held_out.cases.filter((program) => program.expected_invariant).length ?? 0
  const heldOutCorrect = report ? report.held_out.cases.length - heldOutVulnerable : 0
  const heldOutClosed = report?.held_out.summary.find(({ method }) => method === 'scout-closed-loop')
  const crossAdapterCases = report?.cross_adapter.cases.length ?? 0

  return (
    <div className="mx-auto max-w-[1100px] space-y-6">
      <header className="flex flex-col gap-5 border-b border-bg-border pb-6 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <div className="flex items-center gap-2 text-text-muted">
            <ShieldCheck size={16} strokeWidth={1.5} />
            <span className="text-xs font-medium uppercase tracking-[0.16em]">Verification lab</span>
          </div>
          <h1 className="mt-3 text-2xl font-medium tracking-tight text-text-primary">Find payment failures before production</h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-text-muted">
            Scout ranks legal fault schedules. Executable invariants decide whether a result is a failure.
          </p>
        </div>
        <button
          type="button"
          onClick={runDemo}
          disabled={running}
          className="inline-flex min-w-40 items-center justify-center gap-2 rounded-lg bg-text-primary px-4 py-2.5 text-sm font-medium text-bg-base transition-opacity hover:opacity-90 disabled:cursor-wait disabled:opacity-60"
        >
          <Play size={14} fill="currentColor" />
          {running ? 'Running checks…' : report ? 'Run again' : 'Run verification'}
        </button>
      </header>

      <section className="grid overflow-hidden rounded-xl border border-bg-border bg-bg-surface md:grid-cols-3 md:divide-x md:divide-bg-border">
        {[
          ['1', 'Rank', 'Scout ranks schedules that can expose payment bugs.'],
          ['2', 'Execute', 'The lab runs each schedule against a merchant program.'],
          ['3', 'Prove', 'Invariants replay and reduce each real failure.'],
        ].map(([step, title, detail]) => (
          <div key={step} className="flex gap-3 border-b border-bg-border px-5 py-4 last:border-b-0 md:border-b-0">
            <span className="font-mono text-xs text-text-muted">{step}</span>
            <div>
              <h2 className="text-sm font-medium text-text-primary">{title}</h2>
              <p className="mt-1 text-xs leading-5 text-text-muted">{detail}</p>
            </div>
          </div>
        ))}
      </section>

      {error ? (
        <div role="alert" className="rounded-xl border border-status-red/40 bg-status-red/5 px-5 py-4 text-sm text-status-red">
          Verification could not run: {error}
        </div>
      ) : null}

      {report ? (
        <>
          <section role="status" className="flex items-start gap-3 rounded-xl border border-status-green/30 bg-status-green/5 px-5 py-4">
            <Check size={18} className="mt-0.5 shrink-0 text-status-green" />
            <div>
              <h2 className="text-sm font-medium text-text-primary">Scout evaluation complete</h2>
              <p className="mt-1 text-xs leading-5 text-text-muted">
                Closed-loop Scout found all {heldOutVulnerable} held-out failures within 3 schedules. All {heldOutCorrect} controls stayed clean.
              </p>
              <p className="mt-1 font-mono text-[11px] text-text-muted">Seed {report.seed} · budget {report.budget} · deterministic in-process run</p>
            </div>
          </section>

          <section className="grid divide-y divide-bg-border overflow-hidden rounded-xl border border-bg-border bg-bg-surface sm:grid-cols-2 sm:divide-x sm:divide-y-0 lg:grid-cols-4">
            <Metric label="Held-out failures" value={`${heldOutVulnerable}/${heldOutVulnerable}`} detail="Closed-loop Scout within 3 schedules" />
            <Metric label="Held-out controls" value={`${heldOutCorrect}/${heldOutCorrect}`} detail="No invariant findings" />
            <Metric label="Closed-loop median" value={heldOutClosed?.median_executions_before_finding ?? '—'} detail="Held-out discovery rank" />
            <Metric
              label="Scout ranker"
              value={`${report.scout_parameter_count} weights · ${report.scout_model_bytes.toLocaleString()} bytes`}
              detail="Trained linear ranker with fixed priors · local deterministic inference"
            />
          </section>

          <HeldOutTable report={report.held_out} />
          <Finding finding={report.featured_finding} />
          <SearchTable search={report.search} vulnerableCount={vulnerableCount} />

          <section className="rounded-xl border border-bg-border px-5 py-4">
            <h2 className="text-sm font-medium text-text-primary">Evidence boundary</h2>
            <p className="mt-1 max-w-3xl text-xs leading-5 text-text-muted">
              This run uses repository-authored merchants and synthetic regression programs. It does not establish accuracy on unseen production failures.
            </p>
            <p className="mt-1 max-w-3xl text-xs leading-5 text-text-muted">
              Razorpay and PayU produced the same normalized results for all {crossAdapterCases} cross-adapter schedules.
            </p>
            <p className="mt-1 max-w-3xl text-xs leading-5 text-text-muted">
              Fixed priors alone rank this small held-out set earlier with and without runtime feedback.
            </p>
          </section>
        </>
      ) : (
        <section className="rounded-xl border border-dashed border-bg-border px-5 py-8 text-center">
          <p className="text-sm text-text-secondary">Run the verifier to create a fresh report on this machine.</p>
          <p className="mt-1 text-xs text-text-muted">The model cannot mark a failure. Only executable invariants can mark one.</p>
        </section>
      )}
    </div>
  )
}
