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

import type { ReactNode } from 'react'
import { afterEach, test, vi } from 'vitest'

// Mutable fixture state read by the mocked status hook below.
const fixture = vi.hoisted(() => ({
  currentStatus: null as Record<string, unknown> | null,
}))

// External boundaries only: the status hook, the router, and the heavier
// child components that are irrelevant to the sign-up link contract.
vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: fixture.currentStatus,
    loading: false,
    error: null,
  }),
}))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, children }: { to: string; children?: ReactNode }) => (
    <a href={to}>{children}</a>
  ),
  useSearch: () => ({ redirect: undefined }),
}))
vi.mock('../components/user-auth-form', () => ({
  UserAuthForm: () => <div data-testid='user-auth-form' />,
}))
vi.mock('../../auth-layout', () => ({
  AuthLayout: ({ children }: { children?: ReactNode }) => children,
}))
vi.mock('../../components/terms-footer', () => ({
  TermsFooter: () => null,
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SignIn } = await import('../index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {},
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null

async function renderSignIn() {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)

  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <SignIn />
      </I18nextProvider>
    )
  })
}

function signUpLink(): HTMLAnchorElement | null {
  assert.ok(host)
  return host.querySelector('a[href="/sign-up"]')
}

afterEach(async () => {
  if (root) {
    await act(async () => root?.unmount())
  }
  host?.remove()
  root = null
  host = null
  fixture.currentStatus = null
  document.body.replaceChildren()
})

test('hides the sign-up link when password registration is disabled', async () => {
  fixture.currentStatus = {
    register_enabled: true,
    password_register_enabled: false,
  }

  await renderSignIn()

  assert.equal(signUpLink(), null)
  assert.equal(host?.textContent?.includes("Don't have an account?"), false)
})

test('shows the sign-up link when password registration is enabled', async () => {
  fixture.currentStatus = {
    register_enabled: true,
    password_register_enabled: true,
  }

  await renderSignIn()

  assert.ok(signUpLink())
})

test('hides the sign-up link when registration is disabled entirely', async () => {
  fixture.currentStatus = {
    register_enabled: false,
    password_register_enabled: true,
  }

  await renderSignIn()

  assert.equal(signUpLink(), null)
})
