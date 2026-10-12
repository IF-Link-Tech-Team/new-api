import type { PlanRecord } from '@/features/subscriptions/types'

/**
 * Selects exactly one server-provided plan for a public product id.
 * The amount is intentionally never read from the URL or the landing catalog.
 */
export function findTrustedCheckoutPlan(
  productId: string,
  plans: PlanRecord[]
): PlanRecord | null {
  const matches = plans.filter((record) => {
    if (record.product_id !== productId) return false
    if ((record.plan.currency ?? '').trim().toUpperCase() !== 'CNY') {
      return false
    }
    return (
      Number.isFinite(record.plan.price_amount) &&
      record.plan.price_amount >= 0.01
    )
  })
  return matches.length === 1 ? matches[0] : null
}

export function formatCheckoutAmount(plan: PlanRecord): string {
  return `¥${plan.plan.price_amount.toFixed(2)}`
}
