import { RANGE_PRESETS, type TimeRange, type RangeValue } from '../lib/time'

export function TimeRangePicker({ range, onChange }: { range: TimeRange; onChange: (r: { preset: RangeValue }) => void }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <span className="text-muted">Time range</span>
      <select
        className="rounded-md border border-border bg-panel px-2 py-1"
        value={range.preset ?? 'custom'}
        onChange={(e) => onChange({ preset: e.target.value as RangeValue })}
      >
        {range.preset === null && <option value="custom">Custom range</option>}
        {RANGE_PRESETS.map((p) => (
          <option key={p.value} value={p.value}>
            {p.label}
          </option>
        ))}
      </select>
    </label>
  )
}
