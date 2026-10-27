/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { SubscriptionPlan } from '@/features/subscriptions/types'
import { quotaToComputePoints, formatComputePoints } from '@/lib/compute-points'

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
  return plan.title
}

export function getProductComputePoints(
  plan: Pick<SubscriptionPlan, 'total_amount'>
): number | null {
  if (!Number.isFinite(plan.total_amount) || plan.total_amount <= 0) return null
  return quotaToComputePoints(plan.total_amount)
}

export function getProductComputePointsLabel(
  plan: Pick<SubscriptionPlan, 'total_amount'>
): string | null {
  const points = getProductComputePoints(plan)
  return points === null ? null : formatComputePoints(points)
}

export function parseProductDisplay(raw?: string): {
  recommended?: boolean
  button_label?: string
  benefits?: string[]
  badges?: string[]
  image?: string
  image_alt?: string
  reference_price?: string
} {
  try {
    const value: unknown = JSON.parse(raw || '{}')
    if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
    const record = value as Record<string, unknown>
    return {
      recommended: record.recommended === true,
      image:
        typeof record.image === 'string' &&
        (/^https:\/\//i.test(record.image) || /^\/(?!\/)/.test(record.image))
          ? record.image
          : undefined,
      image_alt: typeof record.image_alt === 'string' ? record.image_alt : '',
      reference_price:
        typeof record.reference_price === 'string'
          ? record.reference_price
          : '',
      badges: Array.isArray(record.badges)
        ? [
            ...new Set(
              record.badges.flatMap((badge) =>
                badge &&
                typeof badge === 'object' &&
                typeof badge.label === 'string' &&
                badge.label.trim()
                  ? [badge.label]
                  : []
              )
            ),
          ]
        : [],
      button_label:
        typeof record.button_label === 'string'
          ? record.button_label
          : undefined,
      benefits: Array.isArray(record.benefits)
        ? record.benefits.filter(
            (item): item is string => typeof item === 'string' && item !== ''
          )
        : [],
    }
  } catch {
    return {}
  }
}
