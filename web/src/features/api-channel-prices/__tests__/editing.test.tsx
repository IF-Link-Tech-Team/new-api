/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
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

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const peakPrice =
  'weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && ((hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) || (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18)) ? tier("peak", p * 2 + cr * 0.04 + c * 8) : tier("off_peak", p + cr * 0.02 + c * 4)'

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

it('copies the complete time-based price without overwriting the destination route order', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { revision: 4 } } })
  editor([
    {
      ...rows[0],
      price: {
        ...rows[0].price,
        selling_price: '',
        billing_expression: peakPrice,
      } as ChannelPriceRow['price'],
    },
    rows[1],
  ])
  await user.click(
    screen.getByRole('button', { name: 'Copy prices from first channel' })
  )
  expect(
    screen
      .getAllByRole('switch', { name: 'Time-based or advanced pricing' })
      .every((input) => input.getAttribute('aria-checked') === 'true')
  ).toBe(true)
  expect(screen.getAllByLabelText('Model route position')[1]).toHaveValue('2')
  await user.type(
    screen.getByLabelText('Reason for change'),
    'Keep weekday peak prices'
  )
  await user.click(
    screen.getByRole('button', { name: 'Publish price revision' })
  )
  await waitFor(() => expect(post).toHaveBeenCalled())
  expect(post).toHaveBeenCalledWith(
    '/api/subscription/admin/api-channel-prices',
    expect.objectContaining({
      changes: [
        expect.objectContaining({
          channel_id: 1,
          selling_price: '',
          billing_expression: peakPrice,
        }),
        expect.objectContaining({
          channel_id: 2,
          position: 2,
          selling_price: '',
          billing_expression: peakPrice,
        }),
      ],
    })
  )
})

it('requires a complete fixed price when switching an expression-only price back to fixed pricing', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  editor([
    {
      ...rows[0],
      price: {
        ...rows[0].price,
        selling_price: '',
        billing_expression: peakPrice,
      } as ChannelPriceRow['price'],
    },
  ])
  await user.click(
    screen.getByRole('switch', { name: 'Time-based or advanced pricing' })
  )
  await user.type(
    screen.getByLabelText('Reason for change'),
    'Switch to fixed pricing'
  )
  await user.click(
    screen.getByRole('button', { name: 'Publish price revision' })
  )
  await waitFor(() =>
    expect(
      screen.getAllByText('Enter a finite, non-negative price')
    ).toHaveLength(4)
  )
  expect(post).not.toHaveBeenCalled()
})
