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

import { afterEach, test } from 'vitest'

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { PaymentDisplayDialog } = await import('../payment-display-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'zh',
  resources: {
    zh: {
      translation: {
        'Alipay collection': '支付宝收款',
        'Alipay payment integration is in progress. Online payment is currently unavailable.':
          '支付宝支付接入中，当前暂无法完成在线付款',
        'Alipay QR code': '支付宝收款码',
        'Alipay QR code pending': '支付宝收款码待补充',
        'Amount Due': '应付金额',
        Close: '关闭',
        'Product Name': '产品名称',
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
let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null

afterEach(async () => {
  globalThis.fetch = originalFetch
  if (root) {
    await act(async () => root?.unmount())
  }
  host?.remove()
  root = null
  host = null
  document.body.replaceChildren()
})

test('shows product details and the QR placeholder without creating an order', async () => {
  const requests: string[] = []
  globalThis.fetch = async (input) => {
    requests.push(String(input))
    return {
      ok: true,
      json: async () => ({ alipayQrCodeUrl: '' }),
    } as Response
  }
  const openChanges: boolean[] = []
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)

  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <PaymentDisplayDialog
          open
          onOpenChange={(open) => openChanges.push(open)}
          productName='Pro'
          amount='¥79.00'
        />
      </I18nextProvider>
    )
  })

  const text = document.body.textContent ?? ''
  assert.equal(text.includes('Pro'), true)
  assert.equal(text.includes('¥79.00'), true)
  assert.equal(text.includes('支付宝收款码待补充'), true)
  assert.equal(text.includes('支付宝支付接入中，当前暂无法完成在线付款'), true)
  assert.deepEqual(requests, ['/payment-display.json'])

  const closeButton = [...document.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === '关闭'
  )
  assert.ok(closeButton)
  await act(async () => closeButton.click())
  assert.deepEqual(openChanges, [false])
})
