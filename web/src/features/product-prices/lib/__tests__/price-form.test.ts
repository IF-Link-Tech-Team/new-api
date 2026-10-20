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

import { describe, test } from 'vitest'

import {
  PRICE_FORM_DEFAULTS,
  buildPriceComponents,
  formValuesToPricePayload,
  parseChannelIds,
  parsePriceComponents,
} from '../price-form'

describe('buildPriceComponents / parsePriceComponents', () => {
  test('round-trips the three core components as JSON', () => {
    const raw = buildPriceComponents(1, 0.02, 4)
    assert.deepEqual(parsePriceComponents(raw), {
      input: 1,
      cached_input: 0.02,
      output: 4,
    })
  })

  test('preserves an optional cache_creation component when parsing', () => {
    const parsed = parsePriceComponents(
      '{"input":0.64,"cached_input":0.08,"cache_creation":1,"output":2.16}'
    )
    assert.equal(parsed?.cache_creation, 1)
  })

  test('returns null for empty, malformed, or incomplete payloads', () => {
    assert.equal(parsePriceComponents(''), null)
    assert.equal(parsePriceComponents('not json'), null)
    assert.equal(parsePriceComponents('{"input":1}'), null)
    assert.equal(parsePriceComponents('[1,2,3]'), null)
  })
})

describe('parseChannelIds', () => {
  test('parses a comma-separated list of unique positive IDs', () => {
    assert.deepEqual(parseChannelIds('14, 15'), [14, 15])
  })

  test('rejects empty input, non-numeric entries, duplicates, and non-positive IDs', () => {
    assert.equal(parseChannelIds(''), null)
    assert.equal(parseChannelIds('abc'), null)
    assert.equal(parseChannelIds('15,15'), null)
    assert.equal(parseChannelIds('0'), null)
    assert.equal(parseChannelIds('-3'), null)
  })
})

describe('formValuesToPricePayload', () => {
  test('serializes form values into the immutable price payload', () => {
    const payload = formValuesToPricePayload({
      ...PRICE_FORM_DEFAULTS,
      product_type: 'monthly_subscription',
      model: 'deepseek-v4-flash',
      price_version: 'v2026-10',
      effective_at: 1760000000,
      billing_multiplier: 0.85,
      billing_expression: 'tier("default", p * 1 + cr * 0.2 + c * 4)',
      allowed_channels: '15',
      official_input: 2,
      official_cached_input: 0.04,
      official_output: 8,
      selling_input: 1,
      selling_cached_input: 0.02,
      selling_output: 4,
      upstream_input: 0.8,
      upstream_cached_input: 0.01,
      upstream_output: 3.2,
      cost_input: 1,
      cost_cached_input: 0.02,
      cost_output: 4,
      fallback_allowed: false,
      enabled: true,
    })
    assert.ok(payload)
    assert.equal(payload.price.product_type, 'monthly_subscription')
    assert.equal(payload.price.model, 'deepseek-v4-flash')
    assert.equal(payload.price.price_version, 'v2026-10')
    assert.equal(payload.price.allowed_channels, '[15]')
    assert.deepEqual(JSON.parse(payload.price.user_selling_price as string), {
      input: 1,
      cached_input: 0.02,
      output: 4,
    })
    assert.deepEqual(JSON.parse(payload.price.max_upstream_cost as string), {
      input: 1,
      cached_input: 0.02,
      output: 4,
    })
    assert.equal(payload.price.fallback_allowed, false)
    assert.equal(payload.price.enabled, true)
  })

  test('returns null when the channel list is invalid', () => {
    const payload = formValuesToPricePayload({
      ...PRICE_FORM_DEFAULTS,
      allowed_channels: 'oops',
    })
    assert.equal(payload, null)
  })
})
