/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { getCurrencyDisplay } from './currency'

export const COMPUTE_POINTS_PER_CNY = 2_000

interface QuotaScale {
  quotaPerUnit: number
  cnyPerUnit: number
}

function runtimeQuotaScale(): QuotaScale {
  const { config } = getCurrencyDisplay()
  return {
    quotaPerUnit: config.quotaPerUnit,
    cnyPerUnit: config.usdExchangeRate,
  }
}

/**
 * Convert an RMB-denominated display value into Cilai compute points.
 * This is a presentation unit only and must never be persisted or billed.
 */
export function cnyToComputePoints(cny: number): number {
  if (!Number.isFinite(cny) || cny <= 0) return 0
  return cny * COMPUTE_POINTS_PER_CNY
}

/**
 * Convert internal quota into compute points using the runtime quota scale.
 * New API stores quota in units where QuotaPerUnit is one system currency
 * unit. Cilai's runtime USDExchangeRate is the CNY value of that unit.
 */
export function quotaToComputePoints(
  quota: number,
  scale: QuotaScale = runtimeQuotaScale()
): number {
  if (
    !Number.isFinite(quota) ||
    quota <= 0 ||
    !Number.isFinite(scale.quotaPerUnit) ||
    scale.quotaPerUnit <= 0 ||
    !Number.isFinite(scale.cnyPerUnit) ||
    scale.cnyPerUnit <= 0
  ) {
    return 0
  }

  return (
    (quota / scale.quotaPerUnit) * scale.cnyPerUnit * COMPUTE_POINTS_PER_CNY
  )
}

export function formatComputePoints(points: number): string {
  if (!Number.isFinite(points) || points <= 0) return '0 算力点'

  let maximumFractionDigits = 4
  if (points >= 100) {
    maximumFractionDigits = 0
  } else if (points >= 1) {
    maximumFractionDigits = 2
  }
  const formatted = new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: 0,
    maximumFractionDigits,
  }).format(points)

  return `${formatted} 算力点`
}

export function formatQuotaAsComputePoints(quota: number): string {
  return formatComputePoints(quotaToComputePoints(quota))
}
