/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://token.iflink.tech/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { act, createRef } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { HtmlContent } = await import('@/components/html-content')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore, DEFAULT_CURRENCY_CONFIG } =
  await import('@/stores/system-config-store')
const { api } = await import('@/lib/api')
const { getPricing } = await import('@/features/pricing/api')
const { formatPrice } = await import('@/features/pricing/lib/price')
const { LandingModelZones } = await import('../landing-model-zones')
const { readLandingModelZones } = await import('../../lib/landing-model-zones')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalAdapter = api.defaults.adapter
const catalog = {
  success: true,
  vendors: [],
  usable_group: { default: { desc: 'Wallet', ratio: 1 } },
  group_ratio: { default: 1 },
  data: [
    {
      id: 1,
      model_name: 'deepseek-v4-pro-0813',
      quota_type: 0,
      model_ratio: 1,
      completion_ratio: 2,
      cache_ratio: 0.1,
      enable_groups: ['default'],
    },
  ],
}
const manifest = [
  {
    model_id: 'deepseek-v4-pro-0813',
    name: 'DeepSeek V4 Pro',
    scenario: 'Analysis',
    description: 'Try an analysis task.',
  },
  {
    model_id: 'disabled-model',
    name: 'Unavailable model',
    scenario: 'Disabled',
    description: 'Must be filtered by server catalog.',
  },
]
const content = `<section><div data-landing-models='${JSON.stringify(manifest)}'><p>Static pricing link</p></div></section>`
let root: ReturnType<typeof createRoot> | undefined
let queryClient: InstanceType<typeof QueryClient>
let host: HTMLDivElement
let requests: string[]

async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  const ref = createRef<HTMLDivElement>()
  root = createRoot(host)
  await act(async () =>
    root?.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <div ref={ref}>
            <HtmlContent
              content={content}
              variant='isolated'
              className='custom-home-content'
            />
            <LandingModelZones containerRef={ref} content={content} />
          </div>
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
  const shadow = host.querySelector('.custom-home-content')?.shadowRoot
  assert.ok(shadow)
  return shadow
}

function setUp() {
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  useAuthStore.getState().auth.reset()
  requests = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url || '')
    assert.equal(config.method, 'get')
    assert.equal(config.url, '/api/pricing')
    return { config, status: 200, statusText: 'OK', headers: {}, data: catalog }
  }
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
}

function signIn() {
  useAuthStore.getState().auth.setBundle({
    access_token: 'local-test-access-token',
    token_type: 'Bearer',
    access_expires_at: Date.now() / 1000 + 3600,
    user: { id: 7, username: 'local-test', role: 1, group: 'default' },
    session: {
      sid: 'landing-test-session',
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 0,
      last_active_at: 0,
      expires_at: Date.now() / 1000 + 3600,
    },
  })
}

afterEach(async () => {
  if (root) await act(async () => root?.unmount())
  root = undefined
  queryClient?.clear()
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset()
  document.body.replaceChildren()
})
after(() => domWindow.close())

test('guest sees pricing links without an authenticated API request', async () => {
  setUp()
  const shadow = await mount()
  assert.equal(requests.length, 0)
  assert.match(shadow.textContent || '', /Sign in to view current prices/)
  assert.equal(
    shadow.querySelector('a')?.getAttribute('href'),
    '/pricing?search=deepseek-v4-pro-0813'
  )
  assert.equal(shadow.querySelector('.zone-model-prices'), null)
})

test('signing in shows server prices and filters unavailable models; signing out removes prices', async () => {
  setUp()
  const shadow = await mount()
  await queryClient.prefetchQuery({
    queryKey: ['pricing', 'landing-model-zones', 7, 'landing-test-session'],
    queryFn: getPricing,
  })
  await act(async () => signIn())
  assert.deepEqual(requests, ['/api/pricing'])
  assert.equal(shadow.querySelectorAll('.zone-model-card').length, 1)
  assert.equal(shadow.querySelector('h4')?.textContent, 'DeepSeek V4 Pro')
  const values = [...shadow.querySelectorAll('dd')].map(
    (entry) => entry.textContent
  )
  assert.deepEqual(
    values,
    ['input', 'output', 'cache'].map((type) =>
      formatPrice(
        { ...catalog.data[0], group_ratio: catalog.group_ratio },
        type as 'input' | 'output' | 'cache',
        'M',
        false,
        1,
        1,
        'default'
      )
    )
  )
  await act(async () => useAuthStore.getState().auth.reset())
  assert.equal(shadow.querySelector('.zone-model-prices'), null)
  assert.match(shadow.textContent || '', /Sign in to view current prices/)
})

test('late currency settings refresh already visible model prices', async () => {
  setUp()
  queryClient.setQueryData(
    ['pricing', 'landing-model-zones', 7, 'landing-test-session'],
    catalog
  )
  signIn()
  const shadow = await mount()
  assert.match(shadow.querySelector('dd')?.textContent || '', /^\$/)
  await act(async () =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    })
  )
  assert.match(shadow.querySelector('dd')?.textContent || '', /^¥14/)
})

test('a model outside usable groups is omitted even when present in the server catalog', async () => {
  setUp()
  queryClient.setQueryData(
    ['pricing', 'landing-model-zones', 7, 'landing-test-session'],
    {
      ...catalog,
      usable_group: { other: { desc: 'Other group', ratio: 1 } },
    }
  )
  signIn()
  const shadow = await mount()
  assert.equal(shadow.querySelectorAll('.zone-model-card').length, 0)
  assert.match(shadow.textContent || '', /No models available for this account/)
})

test('complex dynamic prices link to the full rules without inventing a numeric quote', async () => {
  setUp()
  queryClient.setQueryData(
    ['pricing', 'landing-model-zones', 7, 'landing-test-session'],
    {
      ...catalog,
      data: [
        {
          ...catalog.data[0],
          billing_mode: 'tiered_expr',
          billing_expr: 'unsupported_special_expression(p)',
        },
      ],
    }
  )
  signIn()
  const shadow = await mount()
  assert.equal(shadow.querySelectorAll('.zone-model-card').length, 1)
  assert.equal(shadow.querySelector('.zone-model-prices'), null)
  assert.match(
    shadow.querySelector('.zone-model-card')?.textContent || '',
    /Dynamic Pricing/
  )
  assert.equal(
    shadow.querySelector('a')?.getAttribute('href'),
    '/pricing?search=deepseek-v4-pro-0813&group=default'
  )
})

test('invalid custom manifests retain their static fallback instead of being hydrated', () => {
  setUp()
  const container = document.createElement('div')
  container.innerHTML =
    '<div data-landing-models="invalid"><a href="/pricing">Pricing</a></div>'
  assert.deepEqual(readLandingModelZones(container), [])
  assert.equal(container.querySelector('a')?.getAttribute('href'), '/pricing')
})
