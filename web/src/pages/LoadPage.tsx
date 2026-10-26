import { LoadPanel } from '../components/LoadPanel'
import { useNumParam } from '../components/Shell'
import { useRange } from '../lib/scope'

/** One instance's database load (active session sampling) in the page's time range. */
export default function LoadPage() {
  const id = useNumParam('id')
  const [range] = useRange()
  return <LoadPanel targetId={id} range={range} />
}
