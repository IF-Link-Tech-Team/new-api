/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { SubscriptionPlan } from '@/features/subscriptions/types'
import { cnyToComputePoints, formatComputePoints } from '@/lib/compute-points'

export const IF_LEARN_PRODUCT_TYPE = 'pack_deepseek_classroom'
export const IF_LEARN_PRODUCT_NAME = 'IF.Learn 启动包'

export function parseModelScope(modelScope?: string): string[] {
  return [
    ...new Set(
      (modelScope || '')
        .split(',')
        .map((model) => model.trim())
        .filter(Boolean)
    ),
  ]
}

export function isIfLearnPlan(
  plan: Pick<SubscriptionPlan, 'product_type'> | undefined
): boolean {
  return plan?.product_type === IF_LEARN_PRODUCT_TYPE
}

export function getProductDisplayName(
  plan: Pick<SubscriptionPlan, 'product_type' | 'title'>
): string {
  return isIfLearnPlan(plan) ? IF_LEARN_PRODUCT_NAME : plan.title
}

export function getProductComputePoints(
  plan: Pick<SubscriptionPlan, 'currency' | 'price_amount'>
): number | null {
  if (plan.currency !== 'CNY') return null
  return cnyToComputePoints(plan.price_amount)
}

export function getProductComputePointsLabel(
  plan: Pick<SubscriptionPlan, 'currency' | 'price_amount'>
): string | null {
  const points = getProductComputePoints(plan)
  return points === null ? null : formatComputePoints(points)
}
