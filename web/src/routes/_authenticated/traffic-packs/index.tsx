import { createFileRoute } from '@tanstack/react-router'

import { TrafficPacks } from '@/features/traffic-packs'

export const Route = createFileRoute('/_authenticated/traffic-packs/')({
  component: RouteComponent,
})

function RouteComponent() {
  return <TrafficPacks />
}
