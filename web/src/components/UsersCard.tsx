import { useState, type FormEvent } from 'react'
import { Plus } from 'lucide-react'
import { ApiError } from '../api/client'
import { useCreateUser, useDeleteUser, useUpdateUser, useUsers } from '../api/hooks'
import type { Role, User } from '../api/types'
import { ROLE_LABEL, useAuth } from '../auth'
import { ago } from '../lib/format'
import { tableCls, tdCls, Th, trCls } from './Table'
import { Badge, Button, Card, Confirm, Drawer, Field, inputCls, Load, Notice, selectCls } from './ui'

const ROLES: Role[] = ['viewer', 'operator', 'admin']

function NewUser({ onDone }: { onDone: () => void }) {
  const create = useCreateUser()
  const [username, setUsername] = useState('')
  const [role, setRole] = useState<Role>('viewer')
  const [password, setPassword] = useState('')
  const fields = create.error instanceof ApiError ? create.error.fields : {}
  const submit = (e: FormEvent) => {
    e.preventDefault()
    create.mutate({ username, role, password }, { onSuccess: onDone })
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="User name" error={fields.username}>
        <input required maxLength={64} autoComplete="off" className={inputCls} value={username} onChange={(e) => setUsername(e.target.value)} />
      </Field>
      <Field label="Role" error={fields.role}>
        <select className={`${selectCls} w-full`} value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {ROLE_LABEL[r]}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Initial password" hint="12 to 72 characters. Share it securely; the user can change it under Account." error={fields.password}>
        <input type="password" required minLength={12} maxLength={72} autoComplete="new-password" className={inputCls} value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      {create.error && create.error instanceof ApiError && !Object.keys(fields).length && <Notice tone="crit">{create.error.message}</Notice>}
      <Button type="submit" variant="primary" busy={create.isPending}>
        Add user
      </Button>
    </form>
  )
}

function ResetPassword({ user, onDone }: { user: User; onDone: () => void }) {
  const update = useUpdateUser()
  const [password, setPassword] = useState('')
  const fields = update.error instanceof ApiError ? update.error.fields : {}
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        update.mutate({ id: user.id, password }, { onSuccess: onDone })
      }}
      className="space-y-3"
    >
      <p className="text-sm text-ink-2">The user is signed out everywhere and signs in with the new password.</p>
      <Field label="New password" hint="12 to 72 characters" error={fields.password}>
        <input type="password" required minLength={12} maxLength={72} autoComplete="new-password" className={inputCls} value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <Button type="submit" variant="primary" busy={update.isPending}>
        Set password
      </Button>
    </form>
  )
}

/** Users and their roles (administrators only). */
export function UsersCard() {
  const { session } = useAuth()
  const q = useUsers()
  const update = useUpdateUser()
  const del = useDeleteUser()
  const [adding, setAdding] = useState(false)
  const [resetting, setResetting] = useState<User | null>(null)
  const [deleting, setDeleting] = useState<User | null>(null)
  return (
    <Card
      pad={false}
      subtitle="Administrators manage everything; operators can preview rows and run read-only SQL; viewers only read monitoring data."
      actions={
        <Button size="sm" variant="primary" onClick={() => setAdding(true)}>
          <Plus aria-hidden className="h-4 w-4" /> Add user
        </Button>
      }
    >
      {update.error && (
        <div className="px-5 pb-3">
          <Notice tone="crit">{update.error.message}</Notice>
        </div>
      )}
      <Load q={q} label="Loading users">
        {(d) => (
          <div className="overflow-x-auto">
            <table className={tableCls}>
              <caption className="sr-only">Users</caption>
              <thead>
                <tr>
                  <Th className="pl-5">User</Th>
                  <Th>Role</Th>
                  <Th>Status</Th>
                  <Th>Last sign-in</Th>
                  <Th className="pr-5">
                    <span className="sr-only">Actions</span>
                  </Th>
                </tr>
              </thead>
              <tbody>
                {d.users.map((u) => {
                  const self = u.id === session?.user_id
                  const locked = u.managed || self
                  return (
                    <tr key={u.id} className={trCls}>
                      <td className={`${tdCls} pl-5`}>
                        <span className="font-medium">{u.username}</span>
                        {self && <span className="ml-2 text-xs text-muted">(you)</span>}
                        {u.managed && <div className="text-xs text-muted">configured by the environment</div>}
                      </td>
                      <td className={tdCls}>
                        <label className="sr-only" htmlFor={`role-${u.id}`}>
                          Role of {u.username}
                        </label>
                        <select id={`role-${u.id}`} className={`${selectCls} py-1 text-xs`} value={u.role} disabled={locked || update.isPending} onChange={(e) => update.mutate({ id: u.id, role: e.target.value as Role })}>
                          {ROLES.map((r) => (
                            <option key={r} value={r}>
                              {ROLE_LABEL[r]}
                            </option>
                          ))}
                        </select>
                      </td>
                      <td className={tdCls}>{u.disabled ? <Badge tone="warn">disabled</Badge> : <Badge tone="good">active</Badge>}</td>
                      <td className={`${tdCls} text-xs text-muted`}>{u.last_login_at ? ago(u.last_login_at) : 'never'}</td>
                      <td className={`${tdCls} pr-5`}>
                        {!locked && (
                          <div className="flex justify-end gap-1">
                            <Button size="sm" onClick={() => setResetting(u)}>
                              Reset password
                            </Button>
                            <Button size="sm" onClick={() => update.mutate({ id: u.id, disabled: !u.disabled })}>
                              {u.disabled ? 'Enable' : 'Disable'}
                            </Button>
                            <Button size="sm" variant="danger" onClick={() => setDeleting(u)}>
                              Delete
                            </Button>
                          </div>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Load>
      <Drawer open={adding} title="Add user" onClose={() => setAdding(false)}>
        {adding && <NewUser onDone={() => setAdding(false)} />}
      </Drawer>
      <Drawer open={!!resetting} title={resetting ? `Reset the password of ${resetting.username}` : ''} onClose={() => setResetting(null)}>
        {resetting && <ResetPassword user={resetting} onDone={() => setResetting(null)} />}
      </Drawer>
      <Confirm
        open={!!deleting}
        title={deleting ? `Delete ${deleting.username}?` : ''}
        confirmLabel="Delete user"
        danger
        busy={del.isPending}
        onCancel={() => setDeleting(null)}
        onConfirm={() => deleting && del.mutate(deleting.id, { onSuccess: () => setDeleting(null) })}
      >
        The user is signed out and can no longer sign in. Incidents they acknowledged keep their name.
      </Confirm>
    </Card>
  )
}
