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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, cleanup } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, test } from 'vitest'

import { api } from '@/lib/api'

import { Checkout } from '../index'

const originalFetch = globalThis.fetch
const originalAdapter = api.defaults.adapter
const i18n = createInstance()
await i18n.init({ lng: 'en', resources: {} })

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
  api.defaults.adapter = originalAdapter
})

test('loads product price from the backend without creating an order', async () => {
  const requests: string[] = []
  globalThis.fetch = async () =>
    ({ ok: true, json: async () => ({ alipayQrCodeUrl: '' }) }) as Response
  api.defaults.adapter = async (config) => {
    requests.push(`${config.method} ${config.url}`)
    return {
      data: {
        success: true,
        data: {
          product_id: 'pro',
          plan: {
            id: 1,
            title: 'Backend Pro',
            price_amount: 79,
            duration_value: 30,
            model_scope: 'deepseek-v4',
            redemption_enabled: false,
          },
        },
      },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => <Checkout productId='pro' />,
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    </QueryClientProvider>
  )
  expect(await screen.findByText('Backend Pro')).toBeVisible()
  expect(screen.getByText('¥79.00')).toBeVisible()
  expect(screen.getByText('deepseek-v4')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Redeem now' })).toBeDisabled()
  expect(
    screen.getByText('Redemption is currently paused for this product.')
  ).toBeVisible()
  expect(requests).toEqual(['get /api/subscription/products/pro'])
})
