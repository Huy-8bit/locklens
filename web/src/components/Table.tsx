import type { ReactNode } from 'react'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { Button, cx } from './ui'

/** A column header. It is positioned so visually hidden labels stay inside the table's scroll area. */
export function Th({ children, align = 'left', sortKey, sort, order, onSort, className }: {
  children: ReactNode
  align?: 'left' | 'right'
  sortKey?: string
  sort?: string
  order?: 'asc' | 'desc'
  onSort?: (key: string) => void
  className?: string
}) {
  const active = sortKey && sort === sortKey
  return (
    <th scope="col" aria-sort={active ? (order === 'asc' ? 'ascending' : 'descending') : undefined} className={cx('relative whitespace-nowrap px-3 py-2 text-xs font-medium text-muted', align === 'right' ? 'text-right' : 'text-left', className)}>
      {sortKey && onSort ? (
        <button type="button" onClick={() => onSort(sortKey)} className={cx('inline-flex items-center gap-1 hover:text-ink', active && 'text-ink')}>
          {children}
          {active && (order === 'asc' ? <ArrowUp aria-hidden className="h-3 w-3" /> : <ArrowDown aria-hidden className="h-3 w-3" />)}
        </button>
      ) : (
        children
      )}
    </th>
  )
}

export const tableCls = 'w-full border-collapse text-sm'
export const trCls = 'border-t border-line hover:bg-surface-2'
export const tdCls = 'px-3 py-2 align-top'
export const tdNum = 'px-3 py-2 text-right align-top num whitespace-nowrap'

export function Pager({ page, pageSize, total, onPage }: { page: number; pageSize: number; total: number; onPage: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1
  return (
    <div className="flex items-center justify-between gap-2 px-3 py-2 text-xs text-muted">
      <span className="num">
        {from}–{Math.min(total, page * pageSize)} of {total.toLocaleString('en-US')}
      </span>
      <div className="flex items-center gap-1">
        <Button size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>
          Previous
        </Button>
        <span className="num px-2">
          {page} / {pages}
        </span>
        <Button size="sm" disabled={page >= pages} onClick={() => onPage(page + 1)}>
          Next
        </Button>
      </div>
    </div>
  )
}
