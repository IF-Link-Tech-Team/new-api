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

import type { SubscriptionPlan } from '../../types'
import {
  PLAN_FORM_DEFAULTS,
  formValuesToPlanPayload,
  joinModelScope,
  planToFormValues,
  splitModelScope,
} from '../plan-form'

function buildPlan(overrides: Partial<SubscriptionPlan>): SubscriptionPlan {
  return {
    id: 7,
    title: 'Monthly Pro',
    price_amount: 49.9,
    currency: 'CNY',
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'monthly',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 0,
    ...overrides,
  } as SubscriptionPlan
}

describe('plan form model scope conversion', () => {
  test('splits a comma-separated scope into trimmed chips', () => {
    assert.deepEqual(splitModelScope('deepseek-v4-flash, glm-5.2 ,, '), [
      'deepseek-v4-flash',
      'glm-5.2',
    ])
  })

  test('returns an empty list for an empty scope', () => {
    assert.deepEqual(splitModelScope(''), [])
    assert.deepEqual(splitModelScope(undefined), [])
  })

  test('joins chips back to a comma-separated scope', () => {
    assert.equal(joinModelScope(['a', ' b ', '']), 'a,b')
  })

  test('survives a plan -> form -> payload round trip', () => {
    const plan = buildPlan({
      product_type: 'pack_k3',
      product_code: 'k3-pack-49',
      product_family: 'kimi_k3',
      product_tier: 2,
      billing_multiplier: 0.95,
      price_version: 'v2026-10',
      model_scope: 'kimi-k3,deepseek-v4-flash',
      course_eligible: true,
      fallback_allowed: false,
      listed: false,
      redemption_enabled: false,
    })
    const values = planToFormValues(plan)
    assert.deepEqual(values.model_scope, ['kimi-k3', 'deepseek-v4-flash'])
    assert.equal(values.listed, false)
    assert.equal(values.redemption_enabled, false)
    assert.equal(values.course_eligible, true)

    const payload = formValuesToPlanPayload(values)
    assert.equal(payload.plan.product_type, 'pack_k3')
    assert.equal(payload.plan.currency, 'CNY')
    assert.equal(payload.plan.product_code, 'k3-pack-49')
    assert.equal(payload.plan.product_family, 'kimi_k3')
    assert.equal(payload.plan.product_tier, 2)
    assert.equal(payload.plan.billing_multiplier, 0.95)
    assert.equal(payload.plan.price_version, 'v2026-10')
    assert.equal(payload.plan.model_scope, 'kimi-k3,deepseek-v4-flash')
    assert.equal(payload.plan.course_eligible, true)
    assert.equal(payload.plan.fallback_allowed, false)
    assert.equal(payload.plan.listed, false)
    assert.equal(payload.plan.redemption_enabled, false)
  })
})

describe('plan form defaults for legacy plans', () => {
  test('legacy plan without contract fields maps to empty contract values', () => {
    const values = planToFormValues(buildPlan({}))
    assert.equal(values.product_type, '')
    assert.equal(values.product_code, '')
    assert.equal(values.product_tier, 0)
    assert.equal(values.billing_multiplier, 0)
    assert.deepEqual(values.model_scope, [])
    assert.equal(values.course_eligible, false)
    assert.equal(values.fallback_allowed, false)
    assert.equal(values.listed, true)
    assert.equal(values.redemption_enabled, true)
  })

  test('preserves the existing currency when editing a legacy plan', () => {
    const values = planToFormValues(buildPlan({ currency: 'USD' }))
    assert.equal(formValuesToPlanPayload(values).plan.currency, 'USD')
  })

  test('payload of defaults carries an empty legacy product contract', () => {
    const payload = formValuesToPlanPayload(PLAN_FORM_DEFAULTS)
    assert.equal(payload.plan.product_type, '')
    assert.equal(payload.plan.model_scope, '')
    assert.equal(payload.plan.listed, true)
    assert.equal(payload.plan.redemption_enabled, true)
  })
})
