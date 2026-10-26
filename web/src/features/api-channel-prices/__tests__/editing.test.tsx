import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { channelPriceRows, type ChannelPriceRow } from '../api'
import { ChannelPricesEditor } from '../editor'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

afterEach(cleanup)

const rows: ChannelPriceRow[] = [1, 2].map((id) => ({
  channel_id: id,
  channel_name: `Channel ${id}`,
  channel_status: 1,
  model: 'deepseek',
  upstream_model: `vendor-model-${id}`,
  mapping_valid: true,
  cost_verified_at: 0,
  price: {
    id,
    revision: 3,
    model: 'deepseek',
    channel_id: id,
    upstream_model: `vendor-model-${id}`,
    enabled: true,
    position: id,
    selling_price: `{"input":${id},"cached_input":0.1,"cache_creation":1.25,"output":4}`,
  },
}))

function editor(initial: ChannelPriceRow[] = rows) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const close = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <ChannelPricesEditor rows={initial} revision={3} onClose={close} />
    </QueryClientProvider>
  )
  return close
}

it('publishes independent channel prices and the revision loaded with the editor', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { revision: 4 } } })
  const close = editor()
  const inputs = screen.getAllByLabelText('Input')
  await user.clear(inputs[1])
  await user.type(inputs[1], '0')
  await user.type(
    screen.getByLabelText('Reason for change'),
    'Channel B discount'
  )
  await user.click(
    screen.getByRole('button', { name: 'Publish price revision' })
  )
  await waitFor(() => expect(close).toHaveBeenCalled())
  expect(post).toHaveBeenCalledWith(
    '/api/subscription/admin/api-channel-prices',
    expect.objectContaining({
      expected_revision: 3,
      reason: 'Channel B discount',
      changes: [
        expect.objectContaining({
          channel_id: 1,
          selling_price:
            '{"input":1,"cached_input":0.1,"cache_creation":1.25,"output":4}',
        }),
        expect.objectContaining({
          channel_id: 2,
          selling_price:
            '{"input":0,"cached_input":0.1,"cache_creation":1.25,"output":4}',
        }),
      ],
    })
  )
})

it('requires explicit prices before enabling an unpriced channel', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  editor([{ ...rows[0], price: undefined }])
  await user.click(
    screen.getByRole('switch', { name: 'Available for API sales' })
  )
  await user.type(
    screen.getByLabelText('Reason for change'),
    'Sell newly mapped channel'
  )
  await user.click(
    screen.getByRole('button', { name: 'Publish price revision' })
  )
  await waitFor(() =>
    expect(
      screen.getAllByText('Enter a finite, non-negative price').length
    ).toBeGreaterThan(0)
  )
  expect(post).not.toHaveBeenCalled()
})

it('copies four prices as independent editable values while retaining route positions', async () => {
  const user = userEvent.setup()
  editor()
  await user.click(
    screen.getByRole('button', { name: 'Copy prices from first channel' })
  )
  expect(screen.getAllByLabelText('Input')[1]).toHaveValue('1')
  await user.clear(screen.getAllByLabelText('Input')[1])
  await user.type(screen.getAllByLabelText('Input')[1], '3')
  expect(screen.getAllByLabelText('Input')[0]).toHaveValue('1')
  expect(screen.getAllByLabelText('Model route position')[1]).toHaveValue('2')
})

it('retains orphaned prices in the list so administrators can retire them', () => {
  const result = channelPriceRows({
    revision: 3,
    active: false,
    bindings: [],
    prices: rows.flatMap((row) => (row.price ? [row.price] : [])).slice(0, 1),
  })
  expect(result).toEqual([
    expect.objectContaining({
      channel_id: 1,
      mapping_valid: false,
      price: rows[0].price,
    }),
  ])
})

it('does not turn an unconfigured disabled channel into an implicit zero price when saving another channel', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { revision: 4 } } })
  editor([rows[0], { ...rows[1], price: undefined }])
  await user.type(
    screen.getByLabelText('Reason for change'),
    'Update configured channel only'
  )
  await user.click(
    screen.getByRole('button', { name: 'Publish price revision' })
  )
  await waitFor(() => expect(post).toHaveBeenCalled())
  expect(post).toHaveBeenCalledWith(
    '/api/subscription/admin/api-channel-prices',
    expect.objectContaining({
      changes: [expect.objectContaining({ channel_id: 1 })],
    })
  )
})
