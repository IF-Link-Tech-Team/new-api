import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type PriceComponents = {
  input: number
  cached_input: number
  cache_creation: number
  output: number
}

export type APIChannelPrice = {
  id: number
  revision: number
  model: string
  channel_id: number
  upstream_model: string
  selling_price: string
  enabled: boolean
  position: number | null
}

export type ChannelBinding = {
  channel_id: number
  channel_name: string
  channel_status: number
  model: string
  upstream_model: string
  mapping_valid: boolean
  cost?: PriceComponents
  cost_verified_at: number
}

export type APIChannelCatalog = {
  revision: number
  active: boolean
  prices: APIChannelPrice[]
  bindings: ChannelBinding[]
}

export type ChannelPriceRow = ChannelBinding & { price?: APIChannelPrice }

export async function getAPIChannelCatalog(): Promise<APIChannelCatalog> {
  const response = await api.get('/api/subscription/admin/api-channel-prices')
  return requireServerSuccess<{ data: APIChannelCatalog }>(response.data).data
}

export async function publishAPIChannelPrices(request: {
  expected_revision: number
  changes: Omit<APIChannelPrice, 'id' | 'revision'>[]
  reason: string
}): Promise<void> {
  const response = await api.post(
    '/api/subscription/admin/api-channel-prices',
    request
  )
  requireServerSuccess(response.data)
}

export function channelPriceRows(
  catalog: APIChannelCatalog
): ChannelPriceRow[] {
  const rows = new Map<string, ChannelPriceRow>()
  for (const binding of catalog.bindings) {
    rows.set(`${binding.model}:${binding.channel_id}`, { ...binding })
  }
  for (const price of catalog.prices) {
    const key = `${price.model}:${price.channel_id}`
    const binding = rows.get(key) ?? {
      channel_id: price.channel_id,
      channel_name: `#${price.channel_id}`,
      channel_status: 0,
      model: price.model,
      upstream_model: price.upstream_model,
      mapping_valid: false,
      cost_verified_at: 0,
    }
    rows.set(key, { ...binding, price })
  }
  return [...rows.values()]
}
