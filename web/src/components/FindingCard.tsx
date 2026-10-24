import type { Finding } from '../api/types'
import { fmtTime, fmtUnit } from '../lib/format'
import { SeverityBadge, Tag } from './ui'

/** A finding with its evidence, explanation, and limitations. */
export function FindingCard({ finding, compact }: { finding: Finding; compact?: boolean }) {
  const f = finding
  if (f.status === 'insufficient_data') {
    return (
      <li className="rounded-md border border-dashed border-border p-3 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <Tag>not evaluated</Tag>
          <code className="text-xs">{f.rule_id}</code>
        </div>
        <p className="mt-1 text-muted">{f.explanation}</p>
      </li>
    )
  }
  return (
    <li className="rounded-md border border-border p-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <SeverityBadge severity={f.severity} />
        <span className="font-medium">{f.title}</span>
        <code className="text-xs text-muted">
          {f.rule_id} v{f.rule_version}
        </code>
        {f.status === 'resolved' && <Tag tone="ok">resolved</Tag>}
      </div>
      <p className="mt-1 text-xs text-muted">
        Observed {fmtTime(f.observed_from)} – {fmtTime(f.observed_to)}
      </p>
      {f.observed.length > 0 && (
        <ul className="mt-2 list-disc pl-5">
          {f.observed.map((o, i) => (
            <li key={i}>{o}</li>
          ))}
        </ul>
      )}
      {!compact && (
        <>
          <p className="mt-2">{f.explanation}</p>
          {f.evidence.length > 0 && (
            <table className="mt-2 w-full text-left text-xs">
              <caption className="sr-only">Evidence</caption>
              <thead>
                <tr className="text-muted">
                  <th className="py-1 pr-2 font-medium">Evidence</th>
                  <th className="py-1 pr-2 font-medium">Value</th>
                  <th className="py-1 font-medium">Source</th>
                </tr>
              </thead>
              <tbody>
                {f.evidence.map((e, i) => (
                  <tr key={i} className="border-t border-border">
                    <td className="py-1 pr-2">{e.label}</td>
                    <td className="py-1 pr-2 font-mono tabular">{fmtUnit(e.value, e.unit)}</td>
                    <td className="py-1 text-muted">{e.source}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {f.possible_explanations.length > 0 && (
            <div className="mt-2">
              <p className="font-medium">Possible explanations (not confirmed)</p>
              <ul className="list-disc pl-5 text-muted">
                {f.possible_explanations.map((p, i) => (
                  <li key={i}>{p}</li>
                ))}
              </ul>
            </div>
          )}
          {f.next_checks.length > 0 && (
            <div className="mt-2">
              <p className="font-medium">Next checks</p>
              <ul className="list-disc pl-5 text-muted">
                {f.next_checks.map((p, i) => (
                  <li key={i}>{p}</li>
                ))}
              </ul>
            </div>
          )}
          <p className="mt-2 text-xs text-muted">
            <span className="font-medium">Limitations:</span> {f.limitations}
          </p>
        </>
      )}
    </li>
  )
}
