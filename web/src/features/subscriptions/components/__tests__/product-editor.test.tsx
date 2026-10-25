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
import { useState, type ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { ProductChannelOrder } from '@/features/product-prices/components/product-channel-order'
import { ProductPriceVersionDrawer } from '@/features/product-prices/components/product-prices-mutate-drawer'
import { api } from '@/lib/api'

import { subscriptionPlanSchema } from '../../types'
import { ProductDisplayEditor } from '../product-display-editor'
import { ProductPriceBookEditor } from '../product-price-book-editor'
import { SubscriptionsMutateDrawer } from '../subscriptions-mutate-drawer'
import { SubscriptionsProvider } from '../subscriptions-provider'

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})
function Wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>
      <SubscriptionsProvider>{props.children}</SubscriptionsProvider>
    </QueryClientProvider>
  )
}

it('selects channels by name, keeps their model mapping, and reorders the persisted route', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.includes('/channel/search')
        ? {
            items: [
              {
                id: 14,
                name: 'Primary',
                models: 'operator-model',
                status: 1,
                model_mapping: '{"operator-model":"provider-alias"}',
              },
              { id: 15, name: 'Backup', models: 'operator-model', status: 1 },
              { id: 16, name: 'Wrong model', models: 'other-model', status: 1 },
            ],
          }
        : [],
    },
  }))
  function Editor() {
    const [value, setValue] = useState('14')
    return (
      <ProductChannelOrder
        model='operator-model'
        value={value}
        onChange={(next) => {
          setValue(next)
          changed(next)
        }}
      />
    )
  }
  render(<Editor />, { wrapper: Wrapper })
  expect(await screen.findByText('1. Primary (#14)')).toBeVisible()
  expect(screen.getByText('Upstream model: provider-alias')).toBeVisible()
  await user.click(screen.getByRole('combobox'))
  expect(
    screen.queryByRole('option', { name: 'Wrong model (#16)' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: 'Backup (#15)' }))
  expect(changed).toHaveBeenLastCalledWith('14, 15')
  await user.click(screen.getAllByRole('button', { name: 'Move up' })[1])
  expect(changed).toHaveBeenLastCalledWith('15, 14')
  await user.click(screen.getAllByRole('button', { name: 'Remove' })[1])
  expect(changed).toHaveBeenLastCalledWith('15')
})

it('lets an operator choose product order and explains the fallback boundary', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.includes('/channel/search') ? { items: [] } : [],
    },
  }))
  const plan = subscriptionPlanSchema.parse({
    id: 7,
    title: 'Flexible promotion',
    price_amount: 12,
    duration_unit: 'day',
    duration_value: 30,
    quota_reset_period: 'never',
    enabled: false,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 10000,
    price_book: 'product:7',
    config_revision: 3,
  })
  render(
    <ProductPriceVersionDrawer
      open
      onOpenChange={vi.fn()}
      product={plan}
      onCreated={vi.fn()}
    />,
    { wrapper: Wrapper }
  )
  const mode = screen.getByRole('combobox', { name: 'Product route selection' })
  expect(mode).toHaveTextContent('Global channel priority and weight')
  await user.click(mode)
  await user.click(
    screen.getByRole('option', { name: 'Product channel order' })
  )
  expect(mode).toHaveTextContent('Product channel order')
  expect(screen.getByText(/Enter channel IDs in order: primary/)).toBeVisible()
  await user.type(screen.getByLabelText('Allowed Channels'), '15, 14')
  expect(screen.getByLabelText('Allowed Channels')).toHaveValue('15, 14')
  expect(
    screen.getByRole('switch', { name: 'Allow route fallback' })
  ).not.toBeChecked()
})

it('edits product presentation without dropping badge tones or unknown metadata', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  function Editor() {
    const [value, setValue] = useState(
      '{"badges":[{"label":"Sale","tone":"accent"}],"internal_note":"keep"}'
    )
    return (
      <ProductDisplayEditor
        value={value}
        onChange={(next) => {
          setValue(next)
          changed(JSON.parse(next))
        }}
      />
    )
  }
  render(<Editor />)
  await user.type(screen.getByLabelText('Purchase button label'), 'Get it')
  await user.type(
    screen.getByLabelText('Benefits (one per line)'),
    'First{Enter}Second'
  )
  await user.click(screen.getByRole('switch', { name: 'Recommended product' }))
  expect(changed).toHaveBeenLastCalledWith(
    expect.objectContaining({
      button_label: 'Get it',
      benefits: ['First', 'Second'],
      recommended: true,
      badges: [{ label: 'Sale', tone: 'accent' }],
      internal_note: 'keep',
    })
  )
})

it('creates a draft without requesting templates or offering fixed product types', async () => {
  const user = userEvent.setup()
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: url.includes('subscription-product-options')
      ? { message: 'success', data: { products: [] } }
      : { success: true, data: [] },
  }))
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { id: 7 } } })
  render(<SubscriptionsMutateDrawer open onOpenChange={vi.fn()} />, {
    wrapper: Wrapper,
  })
  expect(screen.queryByText('Product Type')).not.toBeInTheDocument()
  expect(screen.queryByText('Template constraints')).not.toBeInTheDocument()
  expect(
    screen.getByRole('switch', { name: 'Enabled Status' })
  ).toHaveAttribute('aria-disabled', 'true')
  expect(
    screen.getByRole('switch', { name: 'Allow redemption codes' })
  ).toHaveAttribute('aria-disabled', 'true')
  await user.type(screen.getByLabelText('Plan Title'), 'Flexible promotion')
  const reservation = screen.getByLabelText(
    'Payment reservation lifetime (seconds)'
  )
  expect(reservation).toHaveValue(1800)
  await user.clear(reservation)
  await user.type(reservation, '2400')
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(post).toHaveBeenCalled())
  expect(post).toHaveBeenCalledWith(
    '/api/subscription/admin/plans',
    expect.objectContaining({
      product_owned_pricing: true,
      plan: expect.objectContaining({
        title: 'Flexible promotion',
        product_type: '',
        billing_multiplier: 1,
        enabled: false,
        listed: false,
        redemption_enabled: false,
        reservation_ttl_seconds: 2400,
      }),
    })
  )
  expect(get.mock.calls.some(([url]) => url.includes('/templates'))).toBe(false)
})

it('requires confirmation to disable a product price and returns the new revision to its editor', async () => {
  const user = userEvent.setup()
  const plan = subscriptionPlanSchema.parse({
    id: 7,
    title: 'Flexible promotion',
    price_amount: 12,
    duration_unit: 'day',
    duration_value: 30,
    quota_reset_period: 'never',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 10000,
    price_book: 'product:7',
    config_revision: 3,
  })
  const price = {
    id: 81,
    price_book: 'product:7',
    model: 'operator-model',
    price_version: 'v1',
    enabled: true,
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.includes('/prices') ? { items: [price], next_id: 0 } : [],
    },
  }))
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({
    data: {
      success: true,
      data: { price: { ...price, enabled: false }, config_revision: 4 },
    },
  })
  const onRevisionChange = vi.fn()
  render(
    <ProductPriceBookEditor
      product={plan}
      revision={3}
      disabled={false}
      onRevisionChange={onRevisionChange}
    />,
    { wrapper: Wrapper }
  )
  await user.click(
    await screen.findByRole('button', {
      name: 'Change sale switch for operator-model v1',
    })
  )
  expect(
    screen.getByText(
      'This changes new sales only. Paid entitlements keep their price version.'
    )
  ).toBeVisible()
  expect(patch).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() => expect(onRevisionChange).toHaveBeenCalledWith(4))
  expect(patch).toHaveBeenCalledWith(
    '/api/subscription/admin/plans/7/prices/81',
    { enabled: false, expected_revision: 3 }
  )
})

it('offers retry after a failed price query and keeps creation disabled while the parent is saving', async () => {
  const user = userEvent.setup()
  const plan = subscriptionPlanSchema.parse({
    id: 7,
    title: 'Flexible promotion',
    price_amount: 12,
    duration_unit: 'day',
    duration_value: 30,
    quota_reset_period: 'never',
    enabled: false,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 10000,
    price_book: 'product:7',
    config_revision: 3,
  })
  let priceUnavailable = true
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.includes('/prices')) {
      if (priceUnavailable) {
        priceUnavailable = false
        throw new Error('isolated fixture unavailable')
      }
      return { data: { success: true, data: { items: [], next_id: 0 } } }
    }
    return { data: { success: true, data: [] } }
  })
  render(
    <ProductPriceBookEditor
      product={plan}
      revision={3}
      disabled
      onRevisionChange={vi.fn()}
    />,
    { wrapper: Wrapper }
  )
  expect(
    screen.getByRole('button', { name: 'Add Price Version' })
  ).toBeDisabled()
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No product model prices yet')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Retry' })
  ).not.toBeInTheDocument()
})
