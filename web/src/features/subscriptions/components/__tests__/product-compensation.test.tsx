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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ProductCompensationDialog } from '../product-compensation-dialog'

let client: QueryClient
const order = {
  id: 21,
  user_id: 31,
  trade_no: 'synthetic/paid-order',
  plan_title: 'Original promotion',
  provisioning_error: 'provisioning_failed',
  paid_money: 17.25,
  paid_currency: 'CNY',
  payment_provider: 'alipay',
  provider_trade_no: 'synthetic-provider-receipt',
}
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'synthetic-root', role: ROLE.SUPER_ADMIN })
})
afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
function Wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

it('requires a reason and preserves the decision after a rejected issuance, then refreshes after success', async () => {
  const user = userEvent.setup()
  let issued = false
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: { items: issued ? [] : [order], next_id: 0 } },
  }))
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: {
        success: false,
        message:
          'product compensation would exceed purchase limit or consume another reservation',
      },
    })
    .mockImplementation(async () => {
      issued = true
      return { data: { success: true, data: { changed: true } } }
    })
  render(<ProductCompensationDialog open onOpenChange={vi.fn()} />, {
    wrapper: Wrapper,
  })
  expect(await screen.findByText('synthetic-provider-receipt')).toBeVisible()
  expect(screen.getByRole('table')).toHaveClass('min-w-3xl')
  await user.click(screen.getByRole('button', { name: 'Retry issuance' }))
  await user.click(screen.getByRole('button', { name: 'Confirm issuance' }))
  expect(await screen.findByText('Enter a compensation reason')).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.type(
    screen.getByRole('textbox', { name: 'Compensation reason' }),
    ' Approved original order '
  )
  await user.click(screen.getByRole('button', { name: 'Confirm issuance' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(screen.getByRole('alertdialog')).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Compensation reason' })
  ).toHaveValue(' Approved original order ')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Confirm issuance' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Confirm issuance' }))
  expect(
    await screen.findByText('No payments awaiting compensation')
  ).toBeVisible()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenLastCalledWith(
    '/api/subscription/admin/compensation-orders/synthetic%2Fpaid-order/retry',
    { note: 'Approved original order' }
  )
})

it('allows an administrator to inspect receipts while keeping issuance unavailable', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'synthetic-admin', role: ROLE.ADMIN })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [order], next_id: 0 } },
  })
  const post = vi.spyOn(api, 'post')
  render(<ProductCompensationDialog open onOpenChange={vi.fn()} />, {
    wrapper: Wrapper,
  })
  expect(await screen.findByText('synthetic-provider-receipt')).toBeVisible()
  expect(
    screen.getByText('Only a super administrator can issue compensation')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Retry issuance' })).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

it('offers retry after a failed list request and fetches the next cursor without losing previous orders', async () => {
  const user = userEvent.setup()
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: { success: false, message: 'isolated fixture unavailable' },
    })
    .mockImplementation(async (url) => ({
      data: {
        success: true,
        data: url.includes('before_id=21')
          ? {
              items: [{ ...order, id: 20, trade_no: 'older-synthetic-order' }],
              next_id: 0,
            }
          : { items: [order], next_id: 21 },
      },
    }))
  render(<ProductCompensationDialog open onOpenChange={vi.fn()} />, {
    wrapper: Wrapper,
  })
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText(order.trade_no)).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Load more' }))
  expect(await screen.findByText('older-synthetic-order')).toBeVisible()
  expect(screen.getByText(order.trade_no)).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Load more' })
  ).not.toBeInTheDocument()
  expect(get).toHaveBeenLastCalledWith(
    '/api/subscription/admin/compensation-orders?limit=50&before_id=21'
  )
})
