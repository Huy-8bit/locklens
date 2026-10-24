import { Link } from 'react-router'
import { Empty } from '../components/ui'

export function NotFoundPage() {
  return (
    <Empty title="Page not found">
      <Link to="/" className="text-accent underline">
        Back to the fleet overview
      </Link>
    </Empty>
  )
}
