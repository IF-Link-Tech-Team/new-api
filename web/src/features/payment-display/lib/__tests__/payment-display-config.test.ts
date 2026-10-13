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
  defaultPaymentDisplayConfig,
  normalizePaymentDisplayConfig,
} from '../payment-display-config'

const bunTestModule = 'bun:test'
const { describe, test } = (await import(bunTestModule)) as {
  describe: typeof import('node:test').describe
  test: typeof import('node:test').test
}

describe('payment display configuration', () => {
  test('keeps the placeholder when the QR code URL is empty', () => {
    assert.deepEqual(
      normalizePaymentDisplayConfig(
        { alipayQrCodeUrl: '  ' },
        'https://token.iflink.tech'
      ),
      defaultPaymentDisplayConfig
    )
  })

  test('resolves one same-origin QR code path for every purchase surface', () => {
    assert.deepEqual(
      normalizePaymentDisplayConfig(
        { alipayQrCodeUrl: '/assets/alipay-collection.png' },
        'https://token.iflink.tech'
      ),
      {
        alipayQrCodeUrl:
          'https://token.iflink.tech/assets/alipay-collection.png',
      }
    )
  })

  test('rejects executable, credentialed, and insecure remote URLs', () => {
    const invalidUrls = [
      'javascript:alert(1)',
      'https://user:secret@example.com/qr.png',
      'http://example.com/qr.png',
    ]

    for (const alipayQrCodeUrl of invalidUrls) {
      assert.deepEqual(
        normalizePaymentDisplayConfig(
          { alipayQrCodeUrl },
          'https://token.iflink.tech'
        ),
        defaultPaymentDisplayConfig
      )
    }
  })
})
