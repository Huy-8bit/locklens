import { Clock } from 'lucide-react'
import { RANGES, type Range } from '../lib/time'

export function RangePicker({ value, onChange }: { value: Range; onChange: (r: Range) => void }) {
  return (
    <label className="inline-flex items-center gap-1.5 text-sm text-ink-2">
      <Clock aria-hidden className="h-4 w-4" />
      <span className="sr-only">Time range</span>
      <select value={value} onChange={(e) => onChange(e.target.value as Range)} className="rounded-lg border border-line-strong bg-surface px-2 py-1.5 text-sm text-ink">
        {RANGES.map((r) => (
          <option key={r.value} value={r.value}>
            {r.label}
          </option>
        ))}
      </select>
    </label>
  )
}
