import { createFileRoute } from '@tanstack/react-router'

import { TrafficPacks } from '@/features/traffic-packs'

export const Route = createFileRoute('/_authenticated/plans/')({
  component: RouteComponent,
})

function RouteComponent() {
  return <TrafficPacks category='subscription' title='套餐' />
}
