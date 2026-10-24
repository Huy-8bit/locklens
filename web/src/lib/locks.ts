import type { WaitingLock } from '../api/types'

/** Describes an ungranted lock request; it never claims which held lock conflicts. */
export function describeLock(l: WaitingLock): string {
  let rel = 'unknown relation'
  if (l.relation) rel = l.relation
  else if (l.relation_oid !== null) rel = `OID ${l.relation_oid}${l.database ? ` in database ${l.database}` : ''}`
  switch (l.locktype) {
    case 'relation':
      return `${l.mode} on relation ${rel}`
    case 'tuple':
      return `${l.mode} on tuple (${l.page ?? '?'},${l.tuple ?? '?'}) of ${rel}`
    case 'page':
      return `${l.mode} on page ${l.page ?? '?'} of ${rel}`
    case 'extend':
      return `${l.mode} to extend ${rel}`
    case 'transactionid':
      return `${l.mode} on transaction ${l.transactionid ?? '?'} (typically a row lock held by that transaction)`
    case 'virtualxid':
      return `${l.mode} on virtual transaction ${l.virtualxid ?? '?'}`
    case 'advisory':
      return `${l.mode} on advisory lock (${l.classid ?? '?'}, ${l.objid ?? '?'}, ${l.objsubid ?? '?'})`
    case 'object':
      return `${l.mode} on object (classid ${l.classid ?? '?'}, objid ${l.objid ?? '?'}, objsubid ${l.objsubid ?? '?'})`
    default:
      return `${l.mode} on ${l.locktype} lock`
  }
}
