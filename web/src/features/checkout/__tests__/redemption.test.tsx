/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { afterEach, test } from 'vitest'

const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createMemoryHistory, createRootRoute, createRouter, RouterProvider } =
  await import('@tanstack/react-router')
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { getSubscriptionProduct } = await import('@/features/subscriptions/api')
const { Checkout } = await import('../index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'zh', resources: {} })

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalAdapter = api.defaults.adapter
let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null

afterEach(async () => {
  api.defaults.adapter = originalAdapter
  if (root) await act(async () => root?.unmount())
  host?.remove()
  root = null
  host = null
  document.body.replaceChildren()
})

test('redeems the selected product when online payment is unavailable', async () => {
  const requests: Array<{ method?: string; url?: string; data?: unknown }> = []
  api.defaults.adapter = async (config) => {
    requests.push({
      method: config.method,
      url: config.url,
      data:
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data,
    })
    let data: unknown
    if (config.url === '/api/user/topup/info') {
      data = {
        success: true,
        data: { enable_iflinkpay_topup: false },
      }
    } else if (config.method === 'get') {
      data = {
        success: true,
        data: {
          product_id: 'lite',
          plan: {
            id: 1,
            title: 'Lite',
            subtitle: '课堂测试',
            price_amount: 19,
            currency: 'CNY',
            duration_unit: 'day',
            duration_value: 30,
            quota_reset_period: 'never',
            enabled: true,
            listed: true,
            redemption_enabled: true,
            sort_order: 1,
            max_purchase_per_user: 1,
            total_amount: 9_500_000,
            product_code: 'lite',
          },
        },
      }
    } else {
      data = {
        success: true,
        data: {
          redemption_id: 10,
          plan_id: 1,
          product_type: 'legacy_subscription',
          product_code: 'lite',
          product_family: 'lite',
          user_subscription_id: 20,
          token_id: 30,
          expires_at: 1_800_000_000,
          amount_total: 9_500_000,
          idempotent: false,
        },
      }
    }
    return { data, status: 200, statusText: 'OK', headers: {}, config }
  }

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  await queryClient.prefetchQuery({
    queryKey: ['subscription-product', 'lite'],
    queryFn: async () => (await getSubscriptionProduct('lite')).data,
  })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  const rootRoute = createRootRoute({
    component: () => <Checkout productId='lite' />,
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    root?.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })

  assert.doesNotMatch(document.body.textContent ?? '', /Alipay/)

  const input = document.querySelector(
    'input[aria-label="Redemption code"]'
  ) as HTMLInputElement | null
  assert.ok(input)
  await act(async () => {
    const setInputValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setInputValue)
    setInputValue.call(input, 'CODE-LITE-TEST')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  const button = [...document.querySelectorAll('button')].find((element) =>
    element.textContent?.includes('Redeem now')
  )
  assert.ok(button)
  assert.equal(button.disabled, false)
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })

  assert.deepEqual(requests, [
    { method: 'get', url: '/api/subscription/products/lite', data: undefined },
    { method: 'get', url: '/api/user/topup/info', data: undefined },
    {
      method: 'post',
      url: '/api/subscription/redeem',
      data: { key: 'CODE-LITE-TEST', product_id: 'lite' },
    },
  ])
  assert.match(
    document.body.textContent ?? '',
    /Redemption successful.*dedicated API key/s
  )
})
