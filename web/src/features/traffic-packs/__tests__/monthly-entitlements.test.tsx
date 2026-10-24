/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { afterEach, expect, test } from 'vitest'

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { TrafficPacks } = await import('../index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const originalAdapter = api.defaults.adapter
let root: ReturnType<typeof createRoot> | null = null

test('shows a released reservation with a verified payment as awaiting compensation, not delivered or unpaid', async () => {
  api.defaults.adapter = async (config) => {
    let data: unknown = []
    if (config.url === '/api/subscription/self') {
      data = { all_subscriptions: [] }
    } else if (config.url?.startsWith('/api/token/')) {
      data = { items: [], total: 0 }
    } else if (config.url?.startsWith('/api/subscription/self/orders')) {
      data = [
        {
          trade_no: 'LATE-PAID',
          plan_title: 'Limited promotion',
          status: 'paid_review',
          provisioning_status: 'compensation_pending',
        },
      ]
    }
    return {
      data: { success: true, data },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const route = createRootRoute({ component: () => <TrafficPacks /> })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () =>
    root?.render(
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    )
  )
  expect(host).toHaveTextContent('Limited promotion')
  expect(host).toHaveTextContent(
    'Payment received; awaiting compensation review'
  )
  expect(host).not.toHaveTextContent('权益与 Key 已到账')
  expect(host).not.toHaveTextContent('等待支付')
})

afterEach(async () => {
  if (root) await act(async () => root?.unmount())
  root = null
  api.defaults.adapter = originalAdapter
  sessionStorage.clear()
  document.body.replaceChildren()
  window.history.replaceState({}, '', '/plans')
})

for (const returnParameter of ['iflinkpay_return', 'alipay_return']) {
  test(`${returnParameter} reloads the provisioned entitlement and shows monthly compute points`, async () => {
    window.history.replaceState(
      {},
      '',
      `/plans?${returnParameter}=SUB-TEST-PAID`
    )
    const requests: string[] = []
    const subscription = {
      id: 352,
      user_id: 1,
      plan_id: 5,
      status: 'active',
      amount_total: 9_950_000,
      amount_used: 500_000,
      start_time: Math.floor(Date.now() / 1000),
      end_time: Math.floor(Date.now() / 1000) + 30 * 86400,
      product_type: 'monthly_subscription',
      product_tier: 1,
      price_amount_snapshot: 19.9,
      model_scope: 'qwen3.8-flash,glm-5.2',
    }
    api.defaults.adapter = async (config) => {
      requests.push(config.url ?? '')
      let data: unknown
      if (config.url === '/api/subscription/plans') {
        data = []
      } else if (config.url === '/api/subscription/self') {
        data = {
          all_subscriptions: [
            {
              subscription,
              plan_title: 'Lite 套餐',
              plan_category: 'subscription',
              billing_multipliers: { 'qwen3.8-flash': 1, 'glm-5.2': 0.7 },
            },
          ],
        }
      } else if (config.url?.startsWith('/api/token/')) {
        data = { items: [], total: 0 }
      } else if (config.url?.includes('trade_no=')) {
        data = [
          {
            trade_no: 'SUB-TEST-PAID',
            status: 'success',
            provisioning_status: 'provisioned',
          },
        ]
      } else if (config.url?.startsWith('/api/subscription/self/orders')) {
        data = []
      } else {
        throw new Error(`Unexpected request ${config.url}`)
      }
      return {
        data: { success: true, data },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    const route = createRootRoute({
      component: () => <TrafficPacks category='subscription' title='套餐' />,
    })
    const router = createRouter({
      routeTree: route,
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
    await router.load()
    const host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
    await act(async () => {
      root?.render(
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      )
    })
    assert.ok(
      requests.includes(
        '/api/subscription/self/orders?limit=20&trade_no=SUB-TEST-PAID'
      )
    )
    assert.ok(
      requests.filter((path) => path === '/api/subscription/self').length >= 2
    )
    assert.equal(window.location.search, '')
    assert.match(host.textContent ?? '', /37,800 算力点/)
    assert.match(host.textContent ?? '', /39,800 算力点/)
    assert.ok(host.querySelector('[aria-label="复制模型 ID qwen3.8-flash"]'))
    assert.ok(host.querySelector('[aria-label="复制模型 ID glm-5.2"]'))
  })
}

test('monthly cards show the three paid quotas and copy server-authorized model IDs before Checkout', async () => {
  const models = [
    'qwen3.8-flash',
    'deepseek-v4-1-flash-260910',
    'deepseek-v4-flash-ga-260731',
    'glm-5.2',
    'kimi-k3',
  ]
  api.defaults.adapter = async (config) => {
    let data: unknown
    if (config.url === '/api/subscription/plans') {
      data = [
        { code: 'lite', price: 19.9, quota: 9_950_000, tier: 1 },
        { code: 'pro', price: 49.9, quota: 24_950_000, tier: 2 },
        { code: 'max', price: 99.9, quota: 49_950_000, tier: 3 },
      ].map((item) => ({
        product_id: item.code,
        billing_multipliers: {
          'qwen3.8-flash': 1,
          'deepseek-v4-1-flash-260910': 0.7,
          'deepseek-v4-flash-ga-260731': 0.7,
          'glm-5.2': 0.7,
          'kimi-k3': 0.95,
        },
        plan: {
          id: item.tier + 4,
          title: item.code,
          price_amount: item.price,
          currency: 'CNY',
          duration_value: 30,
          enabled: true,
          redemption_enabled: true,
          category: 'subscription',
          product_type: 'monthly_subscription',
          product_tier: item.tier,
          model_scope: models.join(','),
          total_amount: item.quota,
          course_eligible: false,
        },
      }))
    } else if (config.url === '/api/subscription/self') {
      data = { all_subscriptions: [] }
    } else if (config.url?.startsWith('/api/token/')) {
      data = { items: [], total: 0 }
    } else if (config.url?.startsWith('/api/subscription/self/orders')) {
      data = []
    } else {
      throw new Error(`Unexpected request ${config.url}`)
    }
    return {
      data: { success: true, data },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const route = createRootRoute({
    component: () => <TrafficPacks category='subscription' title='套餐' />,
  })
  const checkout = createRoute({
    getParentRoute: () => route,
    path: '/checkout',
  })
  const router = createRouter({
    routeTree: route.addChildren([checkout]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    )
  })
  for (const quota of ['39,800', '99,800', '199,800']) {
    assert.match(host.textContent ?? '', new RegExp(`${quota} 算力点`))
  }
  for (const price of ['19.9', '49.9', '99.9']) {
    assert.ok(host.textContent?.includes(`¥${price}`))
  }
  for (const model of models) {
    assert.equal(
      host.querySelectorAll(`[aria-label="复制模型 ID ${model}"]`).length,
      3
    )
  }
  assert.equal(
    host.querySelector('[aria-label="复制模型 ID qwen3.8-max"]'),
    null
  )
  const purchaseButtons = [...host.querySelectorAll('button')].filter(
    (button) => button.textContent === '立即购买'
  )
  assert.equal(purchaseButtons.length, 3)
  await act(async () => purchaseButtons[0]?.click())
  assert.equal(router.state.location.pathname, '/checkout')
  assert.equal(
    new URLSearchParams(router.state.location.searchStr).get('product_id'),
    'lite'
  )
})
