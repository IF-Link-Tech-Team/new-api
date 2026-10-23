/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { Checkout } from '../index'

let queryClient: QueryClient
const originalAdapter = api.defaults.adapter
beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})
afterEach(() => {
  cleanup()
  queryClient.clear()
  api.defaults.adapter = originalAdapter
})

async function renderCheckout(paymentEnabled: boolean) {
  const requests: Array<{ method?: string; url?: string; data?: unknown }> = []
  api.defaults.adapter = async (config) => {
    requests.push({ method: config.method, url: config.url, data: config.data })
    let data: unknown = { success: false, message: 'Payment unavailable' }
    if (config.url === '/api/user/topup/info') {
      data = { success: true, data: { enable_iflinkpay_topup: paymentEnabled } }
    }
    if (config.url === '/api/subscription/products/pro') {
      data = {
        success: true,
        data: {
          product_id: 'pro',
          billing_multipliers: { 'deepseek-v4': 0.6 },
          plan: {
            id: 15,
            title: 'Backend Pro',
            price_amount: 79,
            currency: 'CNY',
            duration_value: 30,
            model_scope: 'deepseek-v4',
            redemption_enabled: false,
          },
        },
      }
    }
    return { data, status: 200, statusText: 'OK', headers: {}, config }
  }
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => <Checkout productId='pro' />,
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByText('Backend Pro')
  return requests
}

test('reads server-owned product price and redemption state without creating an order', async () => {
  const requests = await renderCheckout(false)
  expect(screen.getByText('¥79.00')).toBeVisible()
  expect(screen.getByText('deepseek-v4')).toBeVisible()
  expect(screen.getByText(/0.60/)).toBeVisible()
  expect(screen.getByRole('button', { name: /deepseek-v4/ })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Redeem now' })).toBeDisabled()
  expect(
    screen.getByText('Redemption is currently paused for this product.')
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Pay with IFLinkPay' })
  ).not.toBeInTheDocument()
  expect(requests.every((request) => request.method === 'get')).toBe(true)
})

test('only submits the selected backend plan after the enabled IFLinkPay button is clicked', async () => {
  const user = userEvent.setup()
  const requests = await renderCheckout(true)
  const pay = await screen.findByRole('button', { name: 'Pay with IFLinkPay' })
  expect(requests.every((request) => request.method === 'get')).toBe(true)
  const toast = vi.spyOn((await import('sonner')).toast, 'error')
  await user.click(pay)
  await waitFor(() => expect(toast).toHaveBeenCalled())
  expect(requests.filter((request) => request.method === 'post')).toEqual([
    {
      method: 'post',
      url: '/api/subscription/iflinkpay/pay',
      data: JSON.stringify({ plan_id: 15 }),
    },
  ])
  expect(pay).toBeEnabled()
})
