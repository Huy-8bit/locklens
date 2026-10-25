import { useId } from 'react'
import type { Target } from '../../api/types'
import { selectCls } from '../ui'

/** A compact select of monitored instances (for widgets that work on one). */
export function InstanceSelect({ targets, value, onChange }: { targets: Pick<Target, 'id' | 'name'>[]; value: number; onChange: (id: number) => void }) {
  const id = useId()
  if (targets.length === 0) return null
  if (targets.length === 1) return <span className="text-sm font-medium text-ink">{targets[0]?.name}</span>
  return (
    <>
      <label htmlFor={id} className="sr-only">
        Instance
      </label>
      <select id={id} className={`${selectCls} min-w-36`} value={value} onChange={(e) => onChange(Number(e.target.value))}>
        {targets.map((t) => (
          <option key={t.id} value={t.id}>
            {t.name}
          </option>
        ))}
      </select>
    </>
  )
}
