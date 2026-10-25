import { useMemo, useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { cx } from './ui'

const KEYWORDS = new Set(
  ('select from where and or not in is null as join left right inner outer full cross on group by order having limit offset ' +
    'insert into values update set delete returning with recursive union all distinct case when then else end exists ' +
    'create alter drop table index view begin commit rollback savepoint release truncate grant revoke analyze vacuum ' +
    'explain copy merge using matched like ilike between asc desc nulls first last for share nowait skip locked interval ' +
    'true false default primary key references unique check constraint over partition window filter lateral any some show do call').split(' '),
)

/** Lightweight lexical highlighting of SQL text (display only). */
export function highlight(sql: string): ReactNode[] {
  const out: ReactNode[] = []
  const re = /(--[^\n]*|\/\*[\s\S]*?\*\/)|('(?:[^']|'')*')|(\$\d+)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)|(\s+|.)/g
  let m: RegExpExecArray | null
  let i = 0
  while ((m = re.exec(sql)) !== null) {
    const [tok, com, str, param, n, word] = m
    if (com) out.push(<span key={i++} className="sql-com">{tok}</span>)
    else if (str) out.push(<span key={i++} className="sql-str">{tok}</span>)
    else if (param) out.push(<span key={i++} className="sql-param">{tok}</span>)
    else if (n) out.push(<span key={i++} className="sql-num">{tok}</span>)
    else if (word && KEYWORDS.has(word.toLowerCase())) out.push(<span key={i++} className="sql-kw">{tok}</span>)
    else out.push(tok)
  }
  return out
}

/** SQL text, highlighted; `clamp` shows a single line for tables. */
export function SqlText({ sql, clamp = false, className }: { sql: string; clamp?: boolean; className?: string }) {
  const nodes = useMemo(() => highlight(clamp ? sql.replace(/\s+/g, ' ').trim() : sql), [sql, clamp])
  return (
    <code className={cx('font-mono text-[12.5px] leading-relaxed text-ink', clamp ? 'block truncate' : 'block whitespace-pre-wrap break-words', className)} title={clamp ? sql : undefined}>
      {nodes}
    </code>
  )
}

/** Pretty-prints normalised SQL with line breaks before major clauses. */
export function formatSql(sql: string): string {
  return sql
    .replace(/\s+/g, ' ')
    .replace(/\s+(FROM|WHERE|GROUP BY|ORDER BY|HAVING|LIMIT|OFFSET|RETURNING|VALUES|SET|UNION ALL|UNION|LEFT JOIN|RIGHT JOIN|INNER JOIN|JOIN|ON CONFLICT)\b/gi, '\n$1')
    .replace(/\s+(AND|OR)\s+/gi, '\n  $1 ')
    .trim()
}

export function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        })
      }}
      className="inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-xs text-ink-2 hover:bg-surface-2"
    >
      {done ? <Check aria-hidden className="h-3.5 w-3.5" /> : <Copy aria-hidden className="h-3.5 w-3.5" />}
      {done ? 'Copied' : label}
    </button>
  )
}
