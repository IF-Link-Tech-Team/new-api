/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the
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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, waitFor } from '@testing-library/react'
import { createInstance } from 'i18next'
import { act, createRef } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test } from 'vitest'

import { HtmlContent } from '@/components/html-content'
import { consumePaymentSelection } from '@/features/payment-display/lib/payment-trigger'
import { api } from '@/lib/api'

import { LandingStorefront } from '../landing-storefront'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalAdapter = api.defaults.adapter

const catalogResponse = {
  success: true,
  message: '',
  data: [
    {
      product_id: 'pro',
      title: 'Pro',
      subtitle: '适合稳定使用与高频开发',
      price_amount: 49.9,
      currency: 'CNY',
      duration_unit: 'day',
      duration_value: 30,
      total_amount: 24950000,
      category: 'subscription',
      sort_order: 10,
      model_scope: '',
      enabled: true,
      preview: false,
      display_metadata: {
        badges: [{ label: '主推', tone: 'accent' }],
        benefits: ['99,800 算力点'],
        button_label: '立即购买',
        recommended: true,
      },
    },
    {
      product_id: 'deepseek-bigfish-lite-20261017',
      title: 'DeepSeek 大肥鱼 Lite',
      subtitle: '19,800 算力点',
      price_amount: 9.9,
      currency: 'CNY',
      duration_unit: 'day',
      duration_value: 15,
      total_amount: 4950000,
      category: 'package',
      sort_order: 5,
      model_scope: 'deepseek-v4-flash-ga-260731,deepseek-v4-1-flash-260910',
      enabled: false,
      preview: true,
      display_metadata: {
        zone: 'deepseek-zone',
        preview: true,
        button_label: '暂未开售',
        image: '/branding/bigfish-lite.webp',
        image_alt: '大肥鱼 Lite',
      },
    },
  ],
}

const content = `<section>
<div class="plan-grid" data-storefront="plans"></div>
<div class="bigfish-pack-grid" data-storefront="zones" data-storefront-zone="deepseek-zone"></div>
<div data-storefront="packs"></div>
</section>`

let queryClient: InstanceType<typeof QueryClient>
let host: HTMLDivElement
let requests: string[]
let clicks: Array<string | null>

async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  const ref = createRef<HTMLDivElement>()
  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <div
          ref={ref}
          onClick={(event) => {
            const selection = consumePaymentSelection(event.nativeEvent)
            if (selection) {
              clicks.push(selection.productId)
            }
          }}
        >
          <HtmlContent
            content={content}
            variant='isolated'
            className='custom-home-content'
          />
          <LandingStorefront containerRef={ref} content={content} />
        </div>
      </I18nextProvider>
    </QueryClientProvider>,
    { container: host }
  )
  await waitFor(() =>
    expect(['success', 'error']).toContain(
      queryClient.getQueryState(['storefront-catalog'])?.status
    )
  )
  const shadow = host.querySelector('.custom-home-content')?.shadowRoot
  assert.ok(shadow)
  return shadow
}

function setUp() {
  requests = []
  clicks = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url || '')
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: catalogResponse,
    }
  }
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
}

afterEach(() => {
  cleanup()
  queryClient?.clear()
  api.defaults.adapter = originalAdapter
  document.body.replaceChildren()
})

test('anonymous visitors get live plan cards and disabled preview drafts', async () => {
  setUp()
  const shadow = await mount()
  assert.deepEqual(requests, ['/api/subscription/public/catalog'])

  const planCard = shadow.querySelector('.plan-card')
  assert.ok(planCard)
  assert.ok(planCard.classList.contains('plan-card--recommended'))
  assert.match(planCard.textContent || '', /Pro/)
  assert.match(planCard.textContent || '', /¥49\.9/)
  assert.match(planCard.textContent || '', /\/ 30 天/)
  assert.match(planCard.textContent || '', /99,800 算力点/)
  const buy = planCard.querySelector('a[data-payment-product-id="pro"]')
  assert.ok(buy)
  assert.equal(buy.getAttribute('href'), '/checkout?product_id=pro')
  assert.equal(buy.textContent, '立即购买')

  const zoneCard = shadow.querySelector(
    '[data-bigfish-pack="deepseek-bigfish-lite-20261017"]'
  )
  assert.ok(zoneCard)
  const image = zoneCard.querySelector('img')
  assert.equal(image?.getAttribute('src'), '/branding/bigfish-lite.webp')
  assert.equal(image?.getAttribute('alt'), '大肥鱼 Lite')
  assert.match(zoneCard.textContent || '', /¥9\.90/)
  assert.match(zoneCard.textContent || '', /19,800/)
  const disabled = zoneCard.querySelector('button[disabled]')
  assert.ok(disabled)
  assert.equal(disabled.textContent, '暂未开售')
  assert.equal(
    zoneCard.querySelector('a[data-payment-product-id]'),
    null,
    'preview drafts must not expose a checkout link'
  )

  // No traffic-pack mounts matched: the packs container stays empty because
  // the only package entry is zone-scoped.
  const packsMount = shadow.querySelector('[data-storefront="packs"]')
  assert.ok(packsMount)
  assert.equal(packsMount.children.length, 0)
})

test('a shadow portal buy click starts checkout once and a later click still works', async () => {
  setUp()
  const shadow = await mount()
  const buy = shadow.querySelector('a[data-payment-product-id="pro"]')
  assert.ok(buy)
  await act(async () => {
    buy.dispatchEvent(
      new MouseEvent('click', {
        bubbles: true,
        composed: true,
        cancelable: true,
      })
    )
  })
  assert.deepEqual(clicks, ['pro'])
  await act(async () => {
    buy.dispatchEvent(
      new MouseEvent('click', {
        bubbles: true,
        composed: true,
        cancelable: true,
      })
    )
  })
  assert.deepEqual(clicks, ['pro', 'pro'])
})

test('catalog failure leaves mounts empty instead of rendering stale cards', async () => {
  setUp()
  api.defaults.adapter = async (config) => {
    requests.push(config.url || '')
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: false, message: 'boom', data: null },
    }
  }
  const shadow = await mount()
  assert.deepEqual(requests, ['/api/subscription/public/catalog'])
  assert.equal(shadow.querySelector('.plan-card'), null)
  assert.equal(shadow.querySelector('.bigfish-pack-card'), null)
})
