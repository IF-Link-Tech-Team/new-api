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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import type { SubscriptionPlan, PlanPayload } from '../types'

export function getPlanFormSchema(t: TFunction) {
  return z.object({
    title: z.string().min(1, t('Please enter plan title')),
    subtitle: z.string().optional(),
    currency: z.string(),
    price_amount: z.coerce.number().min(0, t('Please enter amount')),
    duration_unit: z.enum(['year', 'month', 'day', 'hour', 'custom']),
    duration_value: z.coerce.number().min(1),
    custom_seconds: z.coerce.number().min(0).optional(),
    quota_reset_period: z.enum([
      'never',
      'daily',
      'weekly',
      'monthly',
      'custom',
    ]),
    quota_reset_custom_seconds: z.coerce.number().min(0).optional(),
    enabled: z.boolean(),
    sort_order: z.coerce.number(),
    allow_balance_pay: z.boolean(),
    allow_wallet_overflow: z.boolean(),
    max_purchase_per_user: z.coerce.number().min(0),
    reservation_ttl_seconds: z.coerce
      .number()
      .int()
      .min(1)
      .max(Number.MAX_SAFE_INTEGER),
    total_amount: z.coerce.number().min(0),
    upgrade_group: z.string().optional(),
    downgrade_group: z.string().optional(),
    category: z.string().optional(),
    stripe_price_id: z.string().optional(),
    creem_product_id: z.string().optional(),
    waffo_pancake_product_id: z.string().optional(),
    product_type: z.string().optional(),
    product_code: z.string().optional(),
    product_family: z.string().optional(),
    product_tier: z.coerce.number().min(0),
    billing_multiplier: z.coerce.number().min(0),
    price_version: z.string().optional(),
    model_scope: z.array(z.string()),
    course_eligible: z.boolean(),
    fallback_allowed: z.boolean(),
    listed: z.boolean(),
    redemption_enabled: z.boolean(),
  })
}

export type PlanFormValues = z.infer<ReturnType<typeof getPlanFormSchema>>

export const PLAN_FORM_DEFAULTS: PlanFormValues = {
  title: '',
  subtitle: '',
  currency: 'CNY',
  price_amount: 0,
  duration_unit: 'month',
  duration_value: 1,
  custom_seconds: 0,
  quota_reset_period: 'never',
  quota_reset_custom_seconds: 0,
  enabled: false,
  sort_order: 0,
  allow_balance_pay: true,
  allow_wallet_overflow: false,
  max_purchase_per_user: 0,
  reservation_ttl_seconds: 1800,
  total_amount: 0,
  upgrade_group: '',
  downgrade_group: '',
  category: 'package',
  stripe_price_id: '',
  creem_product_id: '',
  waffo_pancake_product_id: '',
  product_type: '',
  product_code: '',
  product_family: '',
  product_tier: 0,
  billing_multiplier: 1,
  price_version: '',
  model_scope: [],
  course_eligible: false,
  fallback_allowed: false,
  listed: false,
  redemption_enabled: false,
}

// Plan model_scope is a comma-separated string on the wire; the form edits it
// as a string array for the MultiSelect chips.
export function splitModelScope(scope: string | undefined): string[] {
  return (scope || '')
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean)
}

export function joinModelScope(models: string[]): string {
  return models
    .map((name) => name.trim())
    .filter(Boolean)
    .join(',')
}

export function planToFormValues(plan: SubscriptionPlan): PlanFormValues {
  return {
    title: plan.title || '',
    subtitle: plan.subtitle || '',
    currency: plan.currency || 'CNY',
    price_amount: Number(plan.price_amount || 0),
    duration_unit: plan.duration_unit || 'month',
    duration_value: Number(plan.duration_value || 1),
    custom_seconds: Number(plan.custom_seconds || 0),
    quota_reset_period: plan.quota_reset_period || 'never',
    quota_reset_custom_seconds: Number(plan.quota_reset_custom_seconds || 0),
    enabled: plan.enabled !== false,
    sort_order: Number(plan.sort_order || 0),
    allow_balance_pay: plan.allow_balance_pay !== false,
    allow_wallet_overflow: plan.allow_wallet_overflow !== false,
    max_purchase_per_user: Number(plan.max_purchase_per_user || 0),
    reservation_ttl_seconds: Number(plan.reservation_ttl_seconds || 1800),
    total_amount: quotaUnitsToDollars(Number(plan.total_amount || 0)),
    upgrade_group: plan.upgrade_group || '',
    category: plan.category || 'package',
    downgrade_group: plan.downgrade_group || '',
    stripe_price_id: plan.stripe_price_id || '',
    creem_product_id: plan.creem_product_id || '',
    waffo_pancake_product_id: plan.waffo_pancake_product_id || '',
    product_type: plan.product_type || '',
    product_code: plan.product_code || '',
    product_family: plan.product_family || '',
    product_tier: Number(plan.product_tier || 0),
    billing_multiplier: Number(plan.billing_multiplier || 0),
    price_version: plan.price_version || '',
    model_scope: splitModelScope(plan.model_scope),
    course_eligible: plan.course_eligible === true,
    fallback_allowed: plan.fallback_allowed === true,
    listed: plan.listed !== false,
    redemption_enabled: plan.redemption_enabled !== false,
  }
}

export function formValuesToPlanPayload(
  values: PlanFormValues,
  currentPlan?: SubscriptionPlan,
  revision?: number
): PlanPayload {
  const productOwned = !currentPlan || !!currentPlan.price_book
  const payload: PlanPayload = {
    plan: {
      ...values,
      price_amount: Number(values.price_amount || 0),
      currency: values.product_type ? 'CNY' : values.currency,
      duration_value: Number(values.duration_value || 0),
      custom_seconds: Number(values.custom_seconds || 0),
      quota_reset_period: values.quota_reset_period || 'never',
      quota_reset_custom_seconds:
        values.quota_reset_period === 'custom'
          ? Number(values.quota_reset_custom_seconds || 0)
          : 0,
      sort_order: Number(values.sort_order || 0),
      max_purchase_per_user: Number(values.max_purchase_per_user || 0),
      total_amount: parseQuotaFromDollars(Number(values.total_amount || 0)),
      upgrade_group: values.upgrade_group || '',
      category: values.category || 'package',
      downgrade_group: values.downgrade_group || '',
      product_type: values.product_type || '',
      product_code: values.product_code?.trim() || '',
      product_family: values.product_family || '',
      product_tier: Number(values.product_tier || 0),
      billing_multiplier: Number(values.billing_multiplier || 0),
      price_version: values.price_version || '',
      model_scope: joinModelScope(values.model_scope),
      course_eligible: values.course_eligible,
      fallback_allowed: values.fallback_allowed,
      listed: values.listed,
      redemption_enabled: values.redemption_enabled,
    },
  }
  if (productOwned) {
    payload.product_owned_pricing = true
    payload.expected_revision = revision ?? currentPlan?.config_revision
    payload.plan.price_book = currentPlan?.price_book
    payload.plan.display_metadata = currentPlan?.display_metadata
    payload.plan.product_type = ''
    payload.plan.product_tier = 0
    payload.plan.billing_multiplier = 1
    payload.plan.course_eligible = false
    if (!currentPlan) {
      payload.plan.enabled = false
      payload.plan.listed = false
      payload.plan.redemption_enabled = false
    }
  } else {
    delete payload.plan.reservation_ttl_seconds
  }
  return payload
}
