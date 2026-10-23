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
const { LoginRequiredDialog } = await import('../login-required-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'zh',
  resources: {
    zh: {
      translation: {
        Cancel: '取消',
        'Go to sign in': '前往登录',
        'Please sign in': '请先登录',
        'Sign in to continue with this purchase.': '请登录后继续购买。',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null

afterEach(async () => {
  if (root) {
    await act(async () => root?.unmount())
  }
  host?.remove()
  root = null
  host = null
  document.body.replaceChildren()
})

test('shows the required login copy and exposes cancel and continue actions', async () => {
  let continueCount = 0
  const openChanges: boolean[] = []
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)

  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <LoginRequiredDialog
          open
          onOpenChange={(open) => openChanges.push(open)}
          onContinue={() => {
            continueCount += 1
          }}
        />
      </I18nextProvider>
    )
  })

  const buttons = [...document.querySelectorAll('button')]
  const cancelButton = buttons.find(
    (button) => button.textContent?.trim() === '取消'
  )
  const continueButton = buttons.find(
    (button) => button.textContent?.trim() === '前往登录'
  )

  assert.equal(document.body.textContent?.includes('请先登录'), true)
  assert.ok(cancelButton)
  assert.ok(continueButton)

  await act(async () => continueButton.click())
  assert.equal(continueCount, 1)

  await act(async () => cancelButton.click())
  assert.deepEqual(openChanges, [false])
})
