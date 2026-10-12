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

import {
  loadPurchaseCatalogProduct,
  purchaseCatalogFromDocument,
  purchaseCatalogProductFromDocument,
} from '../purchase-catalog'

const bunTestModule = 'bun:test'
const { describe, test } = (await import(bunTestModule)) as {
  describe: typeof import('node:test').describe
  test: typeof import('node:test').test
}

const catalog = {
  plans: [
    {
      id: 'pro',
      name: 'Pro',
      description: '稳定使用',
      price: '¥79',
      period: '/ 月',
    },
  ],
  traffic_packs: [
    {
      id: 'deepseek',
      name: 'DeepSeek\n流量包',
      description: '专项使用',
      price: '内测价 ¥19.9',
    },
  ],
}

describe('public purchase catalog', () => {
  test('maps plans to display-only payment details', () => {
    assert.deepEqual(purchaseCatalogFromDocument(catalog, 'subscription'), [
      {
        id: 'pro',
        name: 'Pro',
        description: '稳定使用',
        amount: '¥79/ 月',
      },
    ])
  })

  test('maps traffic packs and normalizes multiline names', () => {
    assert.deepEqual(purchaseCatalogFromDocument(catalog, 'package'), [
      {
        id: 'deepseek',
        name: 'DeepSeek 流量包',
        description: '专项使用',
        amount: '内测价 ¥19.9',
      },
    ])
  })

  test('ignores invalid catalog entries', () => {
    assert.deepEqual(
      purchaseCatalogFromDocument({ plans: [{ id: 1 }] }, 'subscription'),
      []
    )
  })

  test('resolves a checkout product by id from the server document', () => {
    assert.deepEqual(purchaseCatalogProductFromDocument(catalog, 'pro'), {
      id: 'pro',
      name: 'Pro',
      description: '稳定使用',
      amount: '¥79/ 月',
    })
    assert.deepEqual(purchaseCatalogProductFromDocument(catalog, 'deepseek'), {
      id: 'deepseek',
      name: 'DeepSeek 流量包',
      description: '专项使用',
      amount: '内测价 ¥19.9',
    })
    assert.equal(purchaseCatalogProductFromDocument(catalog, 'missing'), null)
  })

  test('loads checkout price from the catalog instead of request parameters', async () => {
    const requests: string[] = []
    const product = await loadPurchaseCatalogProduct('pro', async (input) => {
      requests.push(String(input))
      return {
        ok: true,
        json: async () => catalog,
      } as Response
    })

    assert.equal(product?.amount, '¥79/ 月')
    assert.deepEqual(requests, ['/purchase-catalog.json'])
  })
})
