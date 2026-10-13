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

import { afterEach, test, vi } from 'vitest'

// Mutable fixture state read by the mocked hooks below.
const fixture = vi.hoisted(() => ({
  currentStatus: null as Record<string, unknown> | null,
  turnstileEnabled: false,
}))

// External boundaries only: status/network hooks, the register API, the
// axios-based auth-session stack, and the Turnstile script loader.
vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: fixture.currentStatus,
    loading: false,
    error: null,
  }),
}))
vi.mock('@/features/auth/hooks/use-turnstile', () => ({
  useTurnstile: () => ({
    isTurnstileEnabled: fixture.turnstileEnabled,
    turnstileSiteKey: fixture.turnstileEnabled ? 'test-site-key' : '',
    turnstileToken: '',
    setTurnstileToken: () => undefined,
    validateTurnstile: () => true,
  }),
}))
vi.mock('@/features/auth/hooks/use-email-verification', () => ({
  useEmailVerification: () => ({
    isSending: false,
    secondsLeft: 0,
    isActive: false,
    sendCode: async () => false,
  }),
}))
vi.mock('@/features/auth/hooks/use-auth-redirect', () => ({
  useAuthRedirect: () => ({
    redirectToLogin: () => undefined,
    handleLoginResult: async () => undefined,
  }),
}))
vi.mock('@/features/auth/api', () => ({
  register: async () => ({ success: false }),
  wechatLoginByCode: async () => ({ success: false }),
}))
vi.mock('@/lib/api', () => ({
  isAuthBundle: () => false,
}))
vi.mock('@/components/turnstile', () => ({
  Turnstile: () => <div data-testid='turnstile-widget' />,
}))
// OAuthProviders cascades into the router and login dialogs (useOAuthLogin,
// TelegramLoginDialog); per the task scope it is replaced with a minimal
// placeholder that only marks the OAuth section.
vi.mock('@/features/auth/components/oauth-providers', () => ({
  OAuthProviders: () => (
    <div data-testid='oauth-providers'>OAuth providers</div>
  ),
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SignUpForm } = await import('../sign-up-form')

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

async function renderSignUpForm() {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)

  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <SignUpForm />
      </I18nextProvider>
    )
  })
}

function labelTexts(): string[] {
  assert.ok(host)
  return [...host.querySelectorAll('label')].map(
    (label) => label.textContent?.trim() ?? ''
  )
}

function buttonTexts(): string[] {
  assert.ok(host)
  return [...host.querySelectorAll('button')].map(
    (button) => button.textContent?.trim() ?? ''
  )
}

afterEach(async () => {
  if (root) {
    await act(async () => root?.unmount())
  }
  host?.remove()
  root = null
  host = null
  fixture.currentStatus = null
  fixture.turnstileEnabled = false
  document.body.replaceChildren()
})

test('hides password fields, email verification, turnstile and the submit button when password registration is disabled', async () => {
  fixture.currentStatus = {
    password_register_enabled: false,
    email_verification: true,
  }
  fixture.turnstileEnabled = true

  await renderSignUpForm()

  const labels = labelTexts()
  assert.equal(labels.includes('Username'), false)
  assert.equal(labels.includes('Password'), false)
  assert.equal(labels.includes('Confirm password'), false)
  assert.equal(labels.includes('Email (required for verification)'), false)
  assert.equal(buttonTexts().includes('Create account'), false)
  assert.equal(host?.querySelector('[data-testid="turnstile-widget"]'), null)

  // The OAuth login section (e.g. the IF.Link button) must remain available.
  assert.ok(host?.querySelector('[data-testid="oauth-providers"]'))
})

test('shows password fields, email verification, turnstile and the submit button when password registration is enabled', async () => {
  fixture.currentStatus = {
    password_register_enabled: true,
    email_verification: true,
  }
  fixture.turnstileEnabled = true

  await renderSignUpForm()

  const labels = labelTexts()
  assert.equal(labels.includes('Username'), true)
  assert.equal(labels.includes('Password'), true)
  assert.equal(labels.includes('Confirm password'), true)
  assert.equal(labels.includes('Email (required for verification)'), true)
  assert.equal(buttonTexts().includes('Create account'), true)
  assert.ok(host?.querySelector('[data-testid="turnstile-widget"]'))
})

test('shows password fields and the submit button by default when status omits password_register_enabled', async () => {
  fixture.currentStatus = {}
  fixture.turnstileEnabled = false

  await renderSignUpForm()

  const labels = labelTexts()
  assert.equal(labels.includes('Username'), true)
  assert.equal(labels.includes('Password'), true)
  assert.equal(labels.includes('Confirm password'), true)
  assert.equal(buttonTexts().includes('Create account'), true)
})
