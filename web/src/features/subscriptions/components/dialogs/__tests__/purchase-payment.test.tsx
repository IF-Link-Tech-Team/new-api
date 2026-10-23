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
import type { ReactNode } from 'react'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { subscriptionPlanSchema, type PlanRecord } from '../../../types'
import { SubscriptionPurchaseDialog } from '../subscription-purchase-dialog'

const plan: PlanRecord = {
  product_id: 'test-product',
  plan: subscriptionPlanSchema.parse({
    id: 15,
    title: 'Test product',
    price_amount: 5,
    currency: 'CNY',
    duration_unit: 'day',
    duration_value: 30,
    quota_reset_period: 'never',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 10000,
  }),
}

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => {
  cleanup()
  client.clear()
})
function Wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

it('shows each locally enabled payment provider and hides disabled providers', () => {
  const props = { open: true, onOpenChange: vi.fn(), plan, userQuota: 0 }
  const view = render(
    <SubscriptionPurchaseDialog {...props} enableAlipay enableIFLinkPay />,
    { wrapper: Wrapper }
  )
  expect(screen.getByRole('button', { name: 'Alipay payment' })).toBeEnabled()
  expect(
    screen.getByRole('button', { name: 'Pay with IFLinkPay' })
  ).toBeEnabled()
  view.rerender(<SubscriptionPurchaseDialog {...props} />)
  expect(
    screen.queryByRole('button', { name: 'Alipay payment' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Pay with IFLinkPay' })
  ).not.toBeInTheDocument()
})

it('keeps a rejected IFLinkPay purchase open and permits a retry', async () => {
  const user = userEvent.setup()
  const request = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Payment unavailable' },
  })
  const onOpenChange = vi.fn()
  render(
    <SubscriptionPurchaseDialog
      open
      onOpenChange={onOpenChange}
      plan={plan}
      enableIFLinkPay
    />,
    { wrapper: Wrapper }
  )
  const button = screen.getByRole('button', { name: 'Pay with IFLinkPay' })
  await user.click(button)
  await waitFor(() => expect(button).toBeEnabled())
  await user.click(button)
  expect(request).toHaveBeenCalledTimes(2)
  expect(request).toHaveBeenLastCalledWith('/api/subscription/iflinkpay/pay', {
    plan_id: 15,
  })
  expect(onOpenChange).not.toHaveBeenCalled()
})
