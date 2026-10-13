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
import { z } from 'zod'

// ============================================================================
// Subscription Plan Schema & Types
// ============================================================================

export const subscriptionPlanSchema = z.object({
  id: z.number(),
  title: z.string(),
  subtitle: z.string().optional(),
  price_amount: z.number(),
  currency: z.string().default('USD'),
  duration_unit: z.enum(['year', 'month', 'day', 'hour', 'custom']),
  duration_value: z.number(),
  custom_seconds: z.number().optional(),
  quota_reset_period: z.enum(['never', 'daily', 'weekly', 'monthly', 'custom']),
  quota_reset_custom_seconds: z.number().optional(),
  enabled: z.boolean(),
  listed: z.boolean().optional().default(true),
  redemption_enabled: z.boolean().optional().default(true),
  sort_order: z.number(),
  allow_balance_pay: z.boolean().optional().default(true),
  allow_wallet_overflow: z.boolean().optional().default(true),
  max_purchase_per_user: z.number(),
  total_amount: z.number(),
  upgrade_group: z.string().optional(),
  downgrade_group: z.string().optional(),
  category: z.string().optional(),
  stripe_price_id: z.string().optional(),
  creem_product_id: z.string().optional(),
  waffo_pancake_product_id: z.string().optional(),
  product_type: z.string().optional(),
  product_code: z.string().optional(),
  product_family: z.string().optional(),
  product_tier: z.number().optional(),
  billing_multiplier: z.number().optional(),
  price_version: z.string().optional(),
  model_scope: z.string().optional(),
  course_eligible: z.boolean().optional(),
  fallback_allowed: z.boolean().optional(),
})

export type SubscriptionPlan = z.infer<typeof subscriptionPlanSchema>

export interface PlanRecord {
  plan: SubscriptionPlan
  product_id: string
}

// ============================================================================
// User Subscription Schema & Types
// ============================================================================

export const userSubscriptionSchema = z.object({
  id: z.number(),
  user_id: z.number(),
  plan_id: z.number(),
  status: z.string(),
  source: z.string().optional(),
  start_time: z.number(),
  end_time: z.number(),
  amount_total: z.number(),
  amount_used: z.number(),
  next_reset_time: z.number().optional(),
  model_scope: z.string().optional(),
  product_type: z.string().optional(),
  product_code: z.string().optional(),
  product_family: z.string().optional(),
  product_tier: z.number().optional(),
  billing_multiplier: z.number().optional(),
  price_version: z.string().optional(),
  period_id: z.string().optional(),
  price_amount_snapshot: z.number().optional(),
  currency_snapshot: z.string().optional(),
  course_eligible: z.boolean().optional(),
})

export type UserSubscription = z.infer<typeof userSubscriptionSchema>

export interface UserSubscriptionRecord {
  subscription: UserSubscription
  plan_title?: string
  plan_subtitle?: string
  plan_price_amount?: number
  plan_currency?: string
  plan_category?: string
}

// ============================================================================
// API Request/Response Types
// ============================================================================

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export interface PlanPayload {
  plan: Partial<SubscriptionPlan>
}

export interface SubscriptionPayRequest {
  plan_id: number
  payment_method?: string
}

export interface ProductRedemptionResult {
  redemption_id: number
  plan_id: number
  product_type: string
  product_code: string
  product_family: string
  user_subscription_id: number
  token_id: number
  expires_at: number
  amount_total: number
  idempotent: boolean
}

export interface SubscriptionPayResponse {
  success: boolean
  message?: string
  data?: {
    // Stripe-style hosted checkout link.
    pay_link?: string
    // Waffo Pancake / Creem hosted checkout URL.
    checkout_url?: string
    // Pancake-only: order metadata + self-service buyer session token,
    // surfaced for future flows (refund / cancel from new-api's own UI).
    session_id?: string
    expires_at?: number | string
    order_id?: string
    token?: string
    token_expires_at?: number | string
    pay_url?: string
    trade_no?: string
  }
  url?: string
}

export interface SubscriptionOrderStatus {
  trade_no: string
  status: string
  provisioning_status: string
  provisioning_error?: string
  order_type: 'purchase' | 'upgrade' | string
  product_type: string
  product_code: string
  product_family: string
  source_tier: number
  target_tier: number
  plan_title: string
  money: number
  currency: string
  payment_method: string
  user_subscription_id?: number
  create_time: number
  complete_time: number
}

export interface CreateUserSubscriptionRequest {
  plan_id: number
}

export interface ResetUserSubscriptionsRequest {
  plan_id: number
  advance_reset_time: boolean
}

export interface ResetPlanSubscriptionsRequest {
  advance_reset_time: boolean
}

export interface SubscriptionResetResult {
  plan_id: number
  matched_count: number
  reset_count: number
  user_count: number
  advance_reset_time: boolean
}

// ============================================================================
// Self Subscription Data (user-facing)
// ============================================================================

export interface SelfSubscriptionData {
  billing_preference: string
  subscriptions: UserSubscriptionRecord[]
  all_subscriptions: UserSubscriptionRecord[]
}

// ============================================================================
// Dialog Types
// ============================================================================

export type SubscriptionsDialogType =
  | 'create'
  | 'update'
  | 'toggle-status'
  | 'reset-subscriptions'
