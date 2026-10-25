import { useNavigate, useParams } from 'react-router'
import { useFleetLive } from '../api/hooks'
import { PageHeader } from '../components/Shell'
import { Card, Empty, Load } from '../components/ui'
import { InstanceSelect } from '../components/widgets/InstanceSelect'
import { SqlPanel } from '../components/widgets/SqlPanel'
import { useScope } from '../lib/scope'

export default function SqlPage() {
  const { env } = useScope()
  const live = useFleetLive(env)
  const navigate = useNavigate()
  const wanted = Number(useParams().targetId ?? 0)
  return (
    <>
      <PageHeader title="SQL editor" subtitle="One read-only statement at a time, run with the instance's browsing role (never an administrative one) in a read-only transaction that is always rolled back. At most 500 rows or 1 MiB; statements are never stored." />
      <Load q={live} label="Loading instances">
        {(d) => {
          const list = d.targets.map((t) => t.target)
          const id = list.some((t) => t.id === wanted) ? wanted : (list[0]?.id ?? 0)
          if (!id) return <Empty title="No enabled instance in this environment" />
          return (
            <Card pad={false} className="pt-4">
              <SqlPanel key={id} id={id} toolbar={<InstanceSelect targets={list} value={id} onChange={(n) => navigate(`/sql/${n}`)} />} />
            </Card>
          )
        }}
      </Load>
    </>
  )
}
