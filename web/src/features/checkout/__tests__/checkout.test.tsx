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
import { after as afterAll, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://token.iflink.tech/checkout' })
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLImageElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
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
        'Amount Due (RMB)': '应付金额（人民币）',
        'Pay with IFLinkPay': '使用 IFLinkPay 支付',
        'Product Name': '商品名称',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalAdapter = api.defaults.adapter
let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null

afterEach(async () => {
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

test('renders server-owned product details and starts direct IFLinkPay checkout', async () => {
  const apiRequests: Array<{ method?: string; url?: string }> = []
  api.defaults.adapter = async (config) => {
    apiRequests.push({ method: config.method, url: config.url })
    let data: unknown
    if (config.url === '/api/user/topup/info') {
      data = {
        success: true,
        data: { enable_iflinkpay_topup: true },
      }
    } else if (config.method === 'post') {
      data = {
        success: false,
        message: '支付请求失败',
      }
    } else {
      data = {
        success: true,
        data: {
          product_id: 'deepseek-classroom-5-20261014',
          billing_multipliers: {
            'qwen3.8-flash': 1,
            'deepseek-v4-1-flash-260910': 0.6,
          },
          plan: {
            id: 2,
            title: 'DeepSeek 课堂专享六折包',
            subtitle: '课堂使用',
            price_amount: 5,
            currency: 'CNY',
            duration_unit: 'day',
            duration_value: 10,
            quota_reset_period: 'never',
            enabled: true,
            listed: true,
            redemption_enabled: true,
            sort_order: 2,
            max_purchase_per_user: 1,
            total_amount: 2_500_000,
            product_type: 'pack_deepseek_classroom',
            product_code: 'deepseek-classroom-5-20261014',
            model_scope: 'qwen3.8-flash,deepseek-v4-1-flash-260910',
          },
        },
      }
    }
    return {
      data,
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
    queryKey: ['subscription-product', 'deepseek-classroom-5-20261014'],
    queryFn: async () =>
      (await getSubscriptionProduct('deepseek-classroom-5-20261014')).data,
  })

  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <Checkout productId='deepseek-classroom-5-20261014' />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })

  const text = document.body.textContent ?? ''
  assert.equal(text.includes('IF.Learn 启动包'), true)
  assert.equal(text.includes('¥5.00'), true)
  assert.equal(text.includes('10,000 算力点'), true)
  assert.equal(text.includes('qwen3.8-flash'), true)
  assert.equal(text.includes('Versioned base price ×1.00'), true)
  assert.equal(text.includes('Versioned base price ×0.60'), true)
  assert.equal(text.includes('使用 IFLinkPay 支付'), true)
  assert.equal(text.includes('支付宝'), false)

  const payButton = [...document.querySelectorAll('button')].find((element) =>
    element.textContent?.includes('使用 IFLinkPay 支付')
  )
  assert.ok(payButton)
  await act(async () => {
    payButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  assert.deepEqual(apiRequests, [
    {
      method: 'get',
      url: '/api/subscription/products/deepseek-classroom-5-20261014',
    },
    {
      method: 'get',
      url: '/api/user/topup/info',
    },
    {
      method: 'post',
      url: '/api/subscription/iflinkpay/pay',
    },
  ])
})
