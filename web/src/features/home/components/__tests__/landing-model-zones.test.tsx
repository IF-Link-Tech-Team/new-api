/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, waitFor } from '@testing-library/react'
import { createInstance } from 'i18next'
import { createRef } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, test } from 'vitest'

import { HtmlContent } from '@/components/html-content'
import { api } from '@/lib/api'

import { LandingModelZones } from '../landing-model-zones'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalAdapter = api.defaults.adapter
let client: QueryClient
let response: {
  success: boolean
  data: Array<{ model_name: string; description: string }>
}
let requests: string[]
async function mount() {
  requests = []
  response = {
    success: true,
    data: [{ model_name: 'real-model', description: 'From backend' }],
  }
  api.defaults.adapter = async (config) => {
    requests.push(config.url || '')
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: response,
    }
  }
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const ref = createRef<HTMLDivElement>()
  const content = '<div class="model-grid" data-storefront-models></div>'
  const host = render(
    <QueryClientProvider client={client}>
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
  await waitFor(() =>
    assert.equal(client.getQueryState(['storefront-models'])?.status, 'success')
  )
  const shadow = host.container.querySelector(
    '.custom-home-content'
  )?.shadowRoot
  assert.ok(shadow)
  return shadow
}
afterEach(() => {
  cleanup()
  client?.clear()
  api.defaults.adapter = originalAdapter
})

test('guest model cards contain only names and descriptions returned by the public catalog', async () => {
  const shadow = await mount()
  assert.deepEqual(requests, ['/api/subscription/public/models'])
  assert.equal(shadow.querySelectorAll('.model-card').length, 1)
  assert.match(shadow.textContent || '', /real-model.*From backend/)
  assert.equal(
    shadow.querySelector('a')?.getAttribute('href'),
    '/pricing?search=real-model'
  )
})

test('refetch replaces removed models and descriptions without retaining the static list', async () => {
  const shadow = await mount()
  response = {
    success: true,
    data: [{ model_name: 'new-model', description: 'Updated description' }],
  }
  await client.refetchQueries({ queryKey: ['storefront-models'] })
  await waitFor(() => assert.match(shadow.textContent || '', /new-model/))
  assert.doesNotMatch(shadow.textContent || '', /real-model|From backend/)
  assert.match(shadow.textContent || '', /new-model.*Updated description/)
})

test('failed refresh and an empty catalog show an explicit state instead of stale advertisements', async () => {
  const shadow = await mount()
  response = { success: false, data: [] }
  await client.refetchQueries({ queryKey: ['storefront-models'] })
  await waitFor(() =>
    assert.equal(shadow.querySelectorAll('.model-card').length, 0)
  )
  assert.match(shadow.textContent || '', /Models are temporarily unavailable/)
  response = { success: true, data: [] }
  await client.refetchQueries({ queryKey: ['storefront-models'] })
  await waitFor(() =>
    assert.match(shadow.textContent || '', /No models available/)
  )
})
