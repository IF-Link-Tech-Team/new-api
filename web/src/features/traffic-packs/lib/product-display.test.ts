/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { beforeEach, afterEach, test } from 'vitest'

import {
  useSystemConfigStore,
  DEFAULT_CURRENCY_CONFIG,
} from '@/stores/system-config-store'

import {
  getProductComputePointsLabel,
  getProductDisplayName,
  parseModelScope,
  parseProductDisplay,
} from './product-display'

let previous = useSystemConfigStore.getState().config
beforeEach(() => {
  previous = useSystemConfigStore.getState().config
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})
afterEach(() => useSystemConfigStore.getState().setConfig(previous))

test('uses the configured title and issued quota independently of cash price', () => {
  const plan = {
    title: 'DeepSeek 课堂专享六折包',
    product_type: 'pack_deepseek_classroom',
    currency: 'CNY',
    price_amount: 5,
    total_amount: 2_500_000,
  }

  assert.equal(getProductDisplayName(plan), 'DeepSeek 课堂专享六折包')
  assert.equal(getProductComputePointsLabel(plan), '10,000 算力点')
  const discounted = { ...plan, price_amount: 99 }
  assert.equal(getProductComputePointsLabel(discounted), '10,000 算力点')
})

test('parses exact API model IDs from the configured model scope', () => {
  assert.deepEqual(
    parseModelScope(' qwen3.8-flash,deepseek-v4-1-flash-260910,qwen3.8-flash '),
    ['qwen3.8-flash', 'deepseek-v4-1-flash-260910']
  )
})

test('does not present zero quota as a paid amount', () => {
  assert.equal(getProductComputePointsLabel({ total_amount: 0 }), null)
})

test('reads display controls without trusting malformed JSON or non-string benefits', () => {
  assert.deepEqual(parseProductDisplay('broken'), {})
  assert.deepEqual(parseProductDisplay('[]'), {})
  assert.deepEqual(
    parseProductDisplay(
      '{"recommended":true,"button_label":"Buy","benefits":["One",{},"",5]}'
    ),
    {
      recommended: true,
      button_label: 'Buy',
      benefits: ['One'],
      image: undefined,
      image_alt: '',
      reference_price: '',
      badges: [],
    }
  )
})

test('uses configured card images and badges while rejecting executable image schemes', () => {
  assert.equal(
    parseProductDisplay('{"image":"javascript:alert(1)"}').image,
    undefined
  )
  assert.equal(
    parseProductDisplay('{"image":"//untrusted.example/a"}').image,
    undefined
  )
  const display = parseProductDisplay(
    '{"image":"/branding/fish.webp","image_alt":"Fish","badges":[{"label":"Sale","tone":"accent"},{},null],"reference_price":"Regular 20"}'
  )
  assert.equal(display.image, '/branding/fish.webp')
  assert.equal(display.image_alt, 'Fish')
  assert.deepEqual(display.badges, ['Sale'])
  assert.equal(display.reference_price, 'Regular 20')
})
