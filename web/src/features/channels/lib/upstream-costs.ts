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
export const COST_FIELDS = [
  'input',
  'cached_input',
  'cache_creation',
  'output',
] as const
export type CostField = (typeof COST_FIELDS)[number]
export type CostRates = Partial<Record<CostField, number | null>>
export type PeakCost = {
  prices: CostRates
  weekdays: number[]
  windows: string[]
  offpeak_dates: string[]
}
export type CostEntry = CostRates & { currency?: string; peak?: PeakCost }
export type CostDraft = Record<string, CostEntry>
export function parseUpstreamCosts(raw: string | undefined): CostDraft | null {
  if (!raw?.trim()) return {}
  try {
    const value: unknown = JSON.parse(raw)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    for (const [model, price] of Object.entries(value)) {
      if (
        !model.trim() ||
        !price ||
        typeof price !== 'object' ||
        Array.isArray(price)
      ) {
        return null
      }
    }
    const costs = value as CostDraft
    for (const price of Object.values(costs)) {
      if (!('cached_input' in price)) price.cached_input = 0
      if (!('cache_creation' in price)) price.cache_creation = 0
    }
    return costs
  } catch {
    return null
  }
}
export function isValidUpstreamCosts(raw: string | undefined): boolean {
  const costs = parseUpstreamCosts(raw)
  if (!costs) return false
  const validRates = (price: CostRates) =>
    price &&
    COST_FIELDS.every(
      (key) =>
        typeof price[key] === 'number' &&
        Number.isFinite(price[key]) &&
        Number(price[key]) >= 0
    )
  return Object.values(costs).every((price) => {
    if (
      !validRates(price) ||
      (price.currency !== undefined && !['CNY', 'USD'].includes(price.currency))
    ) {
      return false
    }
    if (price.peak === undefined) return true
    const peak = price.peak
    return (
      peak &&
      validRates(peak.prices) &&
      Array.isArray(peak.weekdays) &&
      peak.weekdays.length > 0 &&
      peak.weekdays.length <= 7 &&
      peak.weekdays.every(
        (day) => Number.isInteger(day) && day >= 1 && day <= 7
      ) &&
      Array.isArray(peak.windows) &&
      peak.windows.length > 0 &&
      peak.windows.length <= 24 &&
      peak.windows.every(
        (window) =>
          /^(?:[01]\d|2[0-3]):[0-5]\d-(?:[01]\d|2[0-3]):[0-5]\d$/.test(
            window
          ) && window.slice(0, 5) < window.slice(6)
      ) &&
      Array.isArray(peak.offpeak_dates) &&
      peak.offpeak_dates.length <= 400 &&
      peak.offpeak_dates.every(
        (date) =>
          /^\d{4}-\d{2}-\d{2}$/.test(date) &&
          Number.isFinite(Date.parse(date)) &&
          new Date(date).toISOString().slice(0, 10) === date
      )
    )
  })
}
