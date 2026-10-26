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
import { toast } from 'sonner'
import { afterEach, expect, it, vi } from 'vitest'

import type { ProductPriceBookVersion } from '@/features/product-prices/types'
import { api } from '@/lib/api'

import { subscriptionPlanSchema } from '../../types'
import { ProductChannelVersionDrawer } from '../product-channel-version-drawer'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const product = subscriptionPlanSchema.parse({
  id: 7,
  title: 'Promotion',
  price_amount: 10,
  currency: 'USD',
  duration_unit: 'month',
  duration_value: 1,
  quota_reset_period: 'never',
  enabled: true,
  sort_order: 0,
  max_purchase_per_user: 0,
  total_amount: 100000,
  model_scope: 'deepseek',
  price_book: 'product:7',
  config_revision: 4,
})

function editor(versions: ProductPriceBookVersion[] = []) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).includes('/prices?')) {
      return { data: { success: true, data: { items: versions, next_id: 0 } } }
    }
    return {
      data: {
        success: true,
        data: {
          revision: 8,
          active: true,
          bindings: [1, 2, 3].map((id) => ({
            channel_id: id,
            channel_name: `Channel ${id}`,
            channel_status: 1,
            model: 'deepseek',
            upstream_model: `vendor-${id}`,
            mapping_valid: true,
            cost_verified_at: 0,
          })),
          prices: [1, 2, 3].map((id) => ({
            id,
            revision: 8,
            channel_id: id,
            model: 'deepseek',
            upstream_model: `vendor-${id}`,
            selling_price: '{}',
            position: id,
            enabled: id !== 3,
          })),
        },
      },
    }
  })
  const close = vi.fn(),
    revision = vi.fn(),
    staged = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <ProductChannelVersionDrawer
        product={product}
        revision={4}
        versions={versions}
        onClose={close}
        onRevisionChange={revision}
        onVersionStaged={staged}
      />
    </QueryClientProvider>
  )
  return { close, revision, staged }
}

it('stages only explicitly allowed API channels with independent multipliers and product order', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { config_revision: 5 } },
  })
  const callbacks = editor()
  const switches = await screen.findAllByRole('switch', { name: 'Allowed' })
  expect(switches).toHaveLength(2)
  expect(screen.queryByText('Channel 3 · #3')).not.toBeInTheDocument()
  await user.click(switches[0])
  await user.click(switches[1])
  const multipliers = screen.getAllByLabelText('Multiplier')
  await user.clear(multipliers[0])
  await user.type(multipliers[0], '0.7')
  await user.clear(multipliers[1])
  await user.type(multipliers[1], '1.2')
  const positions = screen.getAllByLabelText('Route position')
  await user.type(positions[0], '1')
  await user.type(positions[1], '0')
  await user.click(screen.getByRole('switch', { name: 'Allow route fallback' }))
  await user.type(screen.getByLabelText('Price Version'), 'promotion-v2')
  await user.type(screen.getByLabelText('Reason for change'), 'Reviewed offer')
  await user.click(screen.getByRole('button', { name: 'Save draft version' }))
  await waitFor(() => expect(callbacks.close).toHaveBeenCalled())
  expect(post).toHaveBeenCalledWith(
    '/api/subscription/admin/plans/7/channel-versions',
    {
      expected_revision: 4,
      expected_api_revision: 8,
      version: 'promotion-v2',
      fallback_allowed: false,
      reason: 'Reviewed offer',
      models: [
        {
          model: 'deepseek',
          rates: [
            { channel_id: 1, multiplier: 0.7, position: 1 },
            { channel_id: 2, multiplier: 1.2, position: 0 },
          ],
        },
      ],
    }
  )
  expect(callbacks.revision).toHaveBeenCalledWith(5)
  expect(callbacks.staged).toHaveBeenCalledWith('promotion-v2')
})

it('copies one previous version while leaving copied values independently editable', async () => {
  const user = userEvent.setup()
  editor([
    {
      model: 'deepseek',
      price_version: 'v1',
      channel_rates:
        '[{"channel_id":1,"multiplier":0.7,"position":null},{"channel_id":2,"multiplier":0.8,"position":null}]',
    } as ProductPriceBookVersion,
  ])
  await screen.findAllByRole('switch', { name: 'Allowed' })
  await user.click(
    screen.getByRole('button', { name: 'Copy previous product version' })
  )
  const multipliers = screen.getAllByLabelText('Multiplier')
  await waitFor(() => expect(multipliers[0]).toHaveValue('0.7'))
  expect(multipliers[1]).toHaveValue('0.8')
  await user.clear(multipliers[1])
  await user.type(multipliers[1], '1.1')
  expect(multipliers[0]).toHaveValue('0.7')
})

it('keeps the editor open when the server rejects a stale catalog revision', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'catalog changed' },
  })
  const callbacks = editor()
  await user.click(
    (await screen.findAllByRole('switch', { name: 'Allowed' }))[0]
  )
  await user.type(screen.getByLabelText('Price Version'), 'v2')
  await user.type(screen.getByLabelText('Reason for change'), 'Review')
  await user.click(screen.getByRole('button', { name: 'Save draft version' }))
  await waitFor(() => expect(api.post).toHaveBeenCalled())
  expect(callbacks.close).not.toHaveBeenCalled()
  expect(callbacks.staged).not.toHaveBeenCalled()
})

it('copies the full source version across pages instead of only the loaded table page', async () => {
  const user = userEvent.setup()
  editor([
    {
      model: 'deepseek',
      price_version: 'v1',
      channel_rates: '[]',
    } as ProductPriceBookVersion,
  ])
  await screen.findAllByRole('switch', { name: 'Allowed' })
  const get = vi.mocked(api.get)
  const original = get.getMockImplementation()
  if (!original) throw new Error('Missing API fixture')
  get.mockImplementation(async (...args) => {
    const url = String(args[0])
    if (!url.includes('/prices?')) return original(...args)
    const lastPage = url.includes('before_id=90')
    return {
      data: {
        success: true,
        data: {
          items: lastPage
            ? [
                {
                  model: 'deepseek',
                  price_version: 'v1',
                  fallback_allowed: false,
                  channel_rates:
                    '[{"channel_id":2,"multiplier":0.6,"position":null}]',
                },
              ]
            : [],
          next_id: lastPage ? 0 : 90,
        },
      },
    }
  })
  await user.click(
    screen.getByRole('button', { name: 'Copy previous product version' })
  )
  await waitFor(() =>
    expect(screen.getAllByLabelText('Multiplier')[1]).toHaveValue('0.6')
  )
  expect(
    screen.getByRole('switch', { name: 'Allow route fallback' })
  ).not.toBeChecked()
  expect(
    get.mock.calls.filter(([url]) => String(url).includes('/prices?'))
  ).toHaveLength(2)
})

it.each([
  ['legacy model prices', undefined],
  [
    'unavailable channel',
    '[{"channel_id":3,"multiplier":0.7,"position":null}]',
  ],
])(
  'preserves manual edits and reports a copy failure for %s',
  async (_name, channelRates) => {
    const user = userEvent.setup()
    const error = vi.spyOn(toast, 'error')
    editor([
      {
        model: 'deepseek',
        price_version: 'v1',
        channel_rates: channelRates,
      } as ProductPriceBookVersion,
    ])
    const switches = await screen.findAllByRole('switch', { name: 'Allowed' })
    await user.click(switches[0])
    await user.clear(screen.getAllByLabelText('Multiplier')[0])
    await user.type(screen.getAllByLabelText('Multiplier')[0], '0.4')
    await user.click(
      screen.getByRole('button', { name: 'Copy previous product version' })
    )
    await waitFor(() => expect(error).toHaveBeenCalled())
    expect(screen.getAllByLabelText('Multiplier')[0]).toHaveValue('0.4')
    expect(switches[0]).toBeChecked()
  }
)
