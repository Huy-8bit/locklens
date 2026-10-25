import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import { useChangePassword } from '../api/hooks'
import { ROLE_LABEL, useAuth } from '../auth'
import { PageHeader } from '../components/Shell'
import { Button, Card, Field, inputCls, KeyValue, Notice } from '../components/ui'

const ROLE_HELP = {
  admin: 'Everything: users, instances, settings, data, and SQL.',
  operator: 'Reads everything, previews table rows, runs read-only SQL, and acknowledges incidents.',
  viewer: 'Reads dashboards, queries, sessions, and incidents. Cannot preview rows, run SQL, or change anything.',
}

export default function AccountPage() {
  const { session, replace } = useAuth()
  const change = useChangePassword()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [done, setDone] = useState(false)
  if (!session) return null
  const fields = change.error instanceof ApiError ? change.error.fields : {}
  const mismatch = repeat.length > 0 && next !== repeat
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (mismatch) return
    setDone(false)
    change.mutate(
      { current_password: current, new_password: next },
      {
        onSuccess: (s) => {
          replace(s)
          setCurrent('')
          setNext('')
          setRepeat('')
          setDone(true)
        },
      },
    )
  }
  return (
    <>
      <PageHeader title="Account" subtitle="Your sign-in and what your role allows." />
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="Profile">
          <KeyValue
            items={[
              ['User name', session.username],
              ['Role', ROLE_LABEL[session.role]],
              ['Allows', ROLE_HELP[session.role]],
            ]}
          />
        </Card>
        <Card title="Change password">
          {session.managed ? (
            <Notice>This administrator is configured by the server's environment (LOCKLENS_ADMIN_PASSWORD). Change the password there and restart LockLens.</Notice>
          ) : (
            <form onSubmit={submit} className="max-w-sm space-y-3">
              <Field label="Current password" error={fields.current_password}>
                <input type="password" autoComplete="current-password" required className={inputCls} value={current} onChange={(e) => setCurrent(e.target.value)} />
              </Field>
              <Field label="New password" hint="12 to 72 characters" error={fields.new_password}>
                <input type="password" autoComplete="new-password" required minLength={12} maxLength={72} className={inputCls} value={next} onChange={(e) => setNext(e.target.value)} />
              </Field>
              <Field label="Repeat the new password" error={mismatch ? 'The passwords do not match' : undefined}>
                <input type="password" autoComplete="new-password" required className={inputCls} value={repeat} onChange={(e) => setRepeat(e.target.value)} />
              </Field>
              {change.error && !(change.error instanceof ApiError && Object.keys(fields).length) && <Notice tone="crit">{change.error.message}</Notice>}
              {done && <Notice>Password changed. Your other sessions were signed out.</Notice>}
              <Button type="submit" variant="primary" busy={change.isPending} disabled={mismatch}>
                Change password
              </Button>
            </form>
          )}
        </Card>
      </div>
    </>
  )
}
