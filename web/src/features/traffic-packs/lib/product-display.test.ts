/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  getProductComputePointsLabel,
  getProductDisplayName,
  parseModelScope,
} from './product-display'

test('uses the IF.Learn public name and the RMB display conversion', () => {
  const plan = {
    title: 'DeepSeek 课堂专享六折包',
    product_type: 'pack_deepseek_classroom',
    currency: 'CNY',
    price_amount: 5,
  }

  assert.equal(getProductDisplayName(plan), 'IF.Learn 启动包')
  assert.equal(getProductComputePointsLabel(plan), '10,000 算力点')
})

test('parses exact API model IDs from the configured model scope', () => {
  assert.deepEqual(
    parseModelScope(' qwen3.8-flash,deepseek-v4-1-flash-260910,qwen3.8-flash '),
    ['qwen3.8-flash', 'deepseek-v4-1-flash-260910']
  )
})

test('does not guess a compute point value for non-CNY products', () => {
  assert.equal(
    getProductComputePointsLabel({ currency: 'USD', price_amount: 5 }),
    null
  )
})
