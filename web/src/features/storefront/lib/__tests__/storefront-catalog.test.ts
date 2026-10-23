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

import { test } from 'vitest'

import {
  formatStorefrontPeriod,
  formatStorefrontPrice,
  formatStorefrontValidity,
  isStorefrontItemPurchasable,
  normalizeStorefrontCatalog,
  sanitizeStorefrontImagePath,
  storefrontItemsForMount,
} from '../storefront-catalog'

const validPlan = {
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
  model_scope: 'deepseek-v4-flash-ga-260910',
  enabled: true,
  preview: false,
  display_metadata: {
    badges: [{ label: '主推', tone: 'accent' }],
    benefits: ['99,800 算力点'],
    button_label: '立即购买',
    recommended: true,
  },
}

test('catalog normalization keeps well-formed entries and defaults metadata', () => {
  const items = normalizeStorefrontCatalog([
    validPlan,
    { product_id: '', title: 'no id' },
    { product_id: 'x' },
    'garbage',
    { ...validPlan, product_id: 'bare', display_metadata: undefined },
  ])
  assert.equal(items.length, 2)
  const bare = items[1]
  assert.equal(bare.product_id, 'bare')
  assert.deepEqual(bare.display_metadata.badges, [])
  assert.deepEqual(bare.display_metadata.benefits, [])
  assert.equal(bare.display_metadata.preview, false)
})

test('badge tone falls back to neutral for unknown tones', () => {
  const [item] = normalizeStorefrontCatalog([
    {
      ...validPlan,
      display_metadata: {
        badges: [
          { label: 'ok', tone: 'beta' },
          { label: 'weird', tone: 'hotpink' },
          { label: 42 },
        ],
      },
    },
  ])
  assert.deepEqual(item.display_metadata.badges, [
    { label: 'ok', tone: 'beta' },
    { label: 'weird', tone: 'neutral' },
  ])
})

test('image paths must be site-relative without markup characters', () => {
  assert.equal(
    sanitizeStorefrontImagePath('/branding/bigfish-lite.webp'),
    '/branding/bigfish-lite.webp'
  )
  assert.equal(sanitizeStorefrontImagePath('//evil.example/x.webp'), '')
  assert.equal(sanitizeStorefrontImagePath('https://evil.example/x.webp'), '')
  assert.equal(sanitizeStorefrontImagePath('branding/x.webp'), '')
  assert.equal(sanitizeStorefrontImagePath('/x" onerror="alert(1)'), '')
  assert.equal(sanitizeStorefrontImagePath(undefined), '')
})

test('only enabled non-preview items are purchasable', () => {
  assert.equal(
    isStorefrontItemPurchasable({ enabled: true, preview: false }),
    true
  )
  assert.equal(
    isStorefrontItemPurchasable({ enabled: true, preview: true }),
    false
  )
  assert.equal(
    isStorefrontItemPurchasable({ enabled: false, preview: false }),
    false
  )
})

test('mount filtering splits plans, packs, and zone-scoped drafts', () => {
  const items = normalizeStorefrontCatalog([
    validPlan,
    {
      ...validPlan,
      product_id: 'deepseek-classroom-5-20261014',
      category: 'package',
      display_metadata: { featured: true },
    },
    {
      ...validPlan,
      product_id: 'deepseek-bigfish-lite-20261017',
      category: 'package',
      enabled: false,
      preview: true,
      display_metadata: {
        zone: 'deepseek-zone',
        preview: true,
        image: '/branding/bigfish-lite.webp',
      },
    },
  ])
  assert.deepEqual(
    storefrontItemsForMount(items, 'plans').map((item) => item.product_id),
    ['pro']
  )
  assert.deepEqual(
    storefrontItemsForMount(items, 'packs').map((item) => item.product_id),
    ['deepseek-classroom-5-20261014']
  )
  assert.deepEqual(
    storefrontItemsForMount(items, 'zones').map((item) => item.product_id),
    ['deepseek-bigfish-lite-20261017']
  )
  assert.deepEqual(
    storefrontItemsForMount(items, 'zones', 'deepseek-zone').map(
      (item) => item.product_id
    ),
    ['deepseek-bigfish-lite-20261017']
  )
  assert.deepEqual(storefrontItemsForMount(items, 'zones', 'other-zone'), [])
})

test('price, period, and validity formatting matches the legacy landing copy', () => {
  assert.equal(formatStorefrontPrice(19.9, 'CNY'), '¥19.9')
  assert.equal(formatStorefrontPrice(5, 'cny'), '¥5')
  assert.equal(formatStorefrontPrice(10, 'USD'), '$10')
  assert.equal(formatStorefrontPeriod('day', 30), '/ 30 天')
  assert.equal(formatStorefrontPeriod('month', 1), '/ 1 个月')
  assert.equal(formatStorefrontPeriod('year', 1), '/ 1 年')
  assert.equal(formatStorefrontPeriod('custom', 0), '')
  assert.equal(formatStorefrontValidity('day', 15), '15 天')
  assert.equal(formatStorefrontValidity('hour', 12), '12 小时')
})
