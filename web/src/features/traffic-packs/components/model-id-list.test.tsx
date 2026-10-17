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

const domWindow = new Window({ url: 'https://token.iflink.tech/traffic-packs' })
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { TooltipProvider } = await import('@/components/ui/tooltip')
const { ModelIdList } = await import('./model-id-list')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'zh',
  resources: {
    zh: {
      translation: {
        Copied: '已复制',
        'Versioned base price': '版本基准价',
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
  if (root) await act(async () => root?.unmount())
  host?.remove()
  root = null
  host = null
  document.body.replaceChildren()
})

after(() => {
  domWindow.close()
})

async function renderList() {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () =>
    root?.render(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider>
          <ModelIdList
            models={['qwen3.8-flash', 'deepseek-v4-1-flash-260910']}
            billingMultipliers={{
              'qwen3.8-flash': 1,
              'deepseek-v4-1-flash-260910': 0.6,
            }}
          />
        </TooltipProvider>
      </I18nextProvider>
    )
  )
}

test('copies the exact API model ID and renders model-level pricing', async () => {
  let copied = ''
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (value: string) => {
        copied = value
      },
    },
  })
  await renderList()

  assert.match(host?.textContent || '', /版本基准价 ×1\.00/)
  assert.match(host?.textContent || '', /版本基准价 ×0\.60/)
  assert.equal(host?.textContent?.includes('官网价'), false)
  const button = host?.querySelector<HTMLButtonElement>(
    '[aria-label="复制模型 ID qwen3.8-flash"]'
  )
  assert.ok(button)
  await act(async () => button.click())
  assert.equal(copied, 'qwen3.8-flash')
  assert.equal(button.getAttribute('aria-label'), '已复制')
})

test('does not show a copied state when all clipboard methods fail', async () => {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async () => {
        throw new Error('clipboard unavailable')
      },
    },
  })
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: () => false,
  })
  await renderList()

  const button = host?.querySelector<HTMLButtonElement>(
    '[aria-label="复制模型 ID qwen3.8-flash"]'
  )
  assert.ok(button)
  await act(async () => button.click())
  assert.equal(button.getAttribute('aria-label'), '复制模型 ID qwen3.8-flash')
})
