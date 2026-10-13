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
import assert from 'node:assert/strict'

import { Window } from 'happy-dom'

const bunTestModule = 'bun:test'
const { afterAll, afterEach, test } = (await import(bunTestModule)) as {
  afterAll: typeof import('node:test').after
  afterEach: typeof import('node:test').afterEach
  test: typeof import('node:test').test
}

const domWindow = new Window({ url: 'https://token.iflink.tech/checkout' })
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLImageElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { getSubscriptionProduct } = await import('@/features/subscriptions/api')
const { Checkout } = await import('../index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'zh',
  resources: {
    zh: {
      translation: {
        'Alipay payment integration is in progress. Online payment is currently unavailable.':
          '支付宝支付接入中，当前暂无法完成在线付款',
        'Alipay QR code': '支付宝支付二维码',
        'Alipay QR code pending': '支付宝支付二维码待接入',
        'Amount Due (RMB)': '应付金额（人民币）',
        Payment: '支付',
        'Product Name': '商品名称',
        'Review your payment details': '请核对商品与金额',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalFetch = globalThis.fetch
const originalAdapter = api.defaults.adapter
let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null

afterEach(async () => {
  globalThis.fetch = originalFetch
  api.defaults.adapter = originalAdapter
  if (root) {
    await act(async () => root?.unmount())
  }
  host?.remove()
  root = null
  host = null
  document.body.replaceChildren()
})

afterAll(() => {
  domWindow.close()
})

test('renders server-owned product details without creating an order', async () => {
  const fetchRequests: string[] = []
  const apiRequests: Array<{ method?: string; url?: string }> = []
  globalThis.fetch = async (input) => {
    fetchRequests.push(String(input))
    return {
      ok: true,
      json: async () => ({ alipayQrCodeUrl: '' }),
    } as Response
  }
  api.defaults.adapter = async (config) => {
    apiRequests.push({ method: config.method, url: config.url })
    return {
      data: {
        success: true,
        data: {
          product_id: 'pro',
          plan: {
            id: 2,
            title: 'Pro',
            subtitle: '稳定使用',
            price_amount: 79,
            currency: 'CNY',
            duration_unit: 'month',
            duration_value: 1,
            quota_reset_period: 'never',
            enabled: true,
            listed: true,
            redemption_enabled: true,
            sort_order: 2,
            max_purchase_per_user: 1,
            total_amount: 39_500_000,
            product_code: 'pro',
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
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  await queryClient.prefetchQuery({
    queryKey: ['subscription-product', 'pro'],
    queryFn: async () => (await getSubscriptionProduct('pro')).data,
  })

  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <Checkout productId='pro' />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })

  const text = document.body.textContent ?? ''
  assert.equal(text.includes('Pro'), true)
  assert.equal(text.includes('¥79.00'), true)
  assert.equal(text.includes('支付宝支付二维码待接入'), true)
  assert.equal(text.includes('支付宝支付接入中，当前暂无法完成在线付款'), true)
  assert.deepEqual(apiRequests, [
    { method: 'get', url: '/api/subscription/products/pro' },
  ])
  assert.deepEqual(fetchRequests, ['/payment-display.json'])
})
