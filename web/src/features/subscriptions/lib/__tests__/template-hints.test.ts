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

import type { ProductTemplate } from '../../types'
import {
  filterModelScopeOptions,
  findTemplateForProductType,
  formatTemplatePriceRules,
  parseTemplateModelScope,
  parseTemplatePriceRules,
} from '../template-hints'

function buildTemplate(overrides: Partial<ProductTemplate>): ProductTemplate {
  return {
    id: 1,
    product_type: 'monthly_subscription',
    duration_days: 30,
    billing_multiplier: 0.85,
    max_purchase_per_user: 0,
    locked: true,
    currency: 'CNY',
    ...overrides,
  } as ProductTemplate
}

describe('findTemplateForProductType', () => {
  test('returns the template matching the selected product type', () => {
    const templates = [
      buildTemplate({ product_type: 'wallet' }),
      buildTemplate({ id: 2, product_type: 'monthly_subscription' }),
    ]
    assert.equal(
      findTemplateForProductType(templates, 'monthly_subscription')?.id,
      2
    )
  })

  test('returns undefined for empty or unknown product types', () => {
    const templates = [buildTemplate({})]
    assert.equal(findTemplateForProductType(templates, ''), undefined)
    assert.equal(findTemplateForProductType(templates, 'nope'), undefined)
  })
})

describe('parseTemplateModelScope', () => {
  test('parses a JSON array of model IDs', () => {
    const template = buildTemplate({
      model_scope: '["deepseek-v4-flash","glm-5.2"]',
    })
    assert.deepEqual(parseTemplateModelScope(template), [
      'deepseek-v4-flash',
      'glm-5.2',
    ])
  })

  test('treats empty or malformed scope as unrestricted', () => {
    assert.deepEqual(parseTemplateModelScope(buildTemplate({})), [])
    assert.deepEqual(
      parseTemplateModelScope(buildTemplate({ model_scope: 'not json' })),
      []
    )
    assert.deepEqual(
      parseTemplateModelScope(buildTemplate({ model_scope: '{"a":1}' })),
      []
    )
    assert.deepEqual(parseTemplateModelScope(null), [])
  })
})

describe('filterModelScopeOptions', () => {
  const enabled = ['deepseek-v4-flash', 'glm-5.2', 'kimi-k3']

  test('keeps every enabled model when the template has no scope', () => {
    assert.deepEqual(filterModelScopeOptions(enabled, undefined), enabled)
  })

  test('filters enabled models down to the template scope', () => {
    const template = buildTemplate({
      model_scope: '["glm-5.2","kimi-k3"]',
    })
    assert.deepEqual(filterModelScopeOptions(enabled, template), [
      'glm-5.2',
      'kimi-k3',
    ])
  })
})

describe('parseTemplatePriceRules', () => {
  test('parses valid price rules', () => {
    const template = buildTemplate({
      price_rules:
        '[{"tier":1,"price_amount":19.9,"total_amount":9950000},{"tier":2,"price_amount":49.9}]',
    })
    const rules = parseTemplatePriceRules(template)
    assert.equal(rules.length, 2)
    assert.equal(rules[0].price_amount, 19.9)
    assert.equal(rules[1].tier, 2)
  })

  test('returns no rules for malformed payloads or rule items without a price', () => {
    assert.deepEqual(
      parseTemplatePriceRules(buildTemplate({ price_rules: 'oops' })),
      []
    )
    assert.deepEqual(
      parseTemplatePriceRules(buildTemplate({ price_rules: '[{"tier":1}]' })),
      []
    )
    assert.deepEqual(parseTemplatePriceRules(null), [])
  })
})

describe('formatTemplatePriceRules', () => {
  test('deduplicates, sorts, and prefixes CNY prices with the yuan sign', () => {
    const template = buildTemplate({
      price_rules:
        '[{"price_amount":49.9},{"price_amount":19.9},{"price_amount":49.9}]',
    })
    assert.equal(formatTemplatePriceRules(template), '¥19.9 / ¥49.9')
  })

  test('uses the dollar sign for non-CNY templates and empty string without rules', () => {
    const usd = buildTemplate({
      currency: 'USD',
      price_rules: '[{"price_amount":5}]',
    })
    assert.equal(formatTemplatePriceRules(usd), '$5')
    assert.equal(
      formatTemplatePriceRules(buildTemplate({ price_rules: '[]' })),
      ''
    )
  })
})
