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

import { paymentSelectionFromEventPath } from '../payment-trigger'

const bunTestModule = 'bun:test'
const { describe, test } = (await import(bunTestModule)) as {
  describe: typeof import('node:test').describe
  test: typeof import('node:test').test
}

function trigger(attributes: Record<string, string>): EventTarget {
  return {
    getAttribute(name: string) {
      return attributes[name] ?? null
    },
  } as unknown as EventTarget
}

describe('public home payment trigger', () => {
  test('reads product details from a composed shadow-DOM event path', () => {
    const selection = paymentSelectionFromEventPath([
      trigger({}),
      trigger({
        'data-payment-product': 'Pro',
        'data-payment-amount': '¥79',
      }),
    ])

    assert.deepEqual(selection, {
      productName: 'Pro',
      amount: '¥79',
    })
  })

  test('ignores ordinary links and incomplete payment metadata', () => {
    assert.equal(
      paymentSelectionFromEventPath([
        trigger({ href: '/plans' }),
        trigger({ 'data-payment-product': 'Pro' }),
      ]),
      null
    )
  })
})
