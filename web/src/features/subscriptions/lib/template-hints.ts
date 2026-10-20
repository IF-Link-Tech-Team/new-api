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
import type { ProductTemplate, ProductTemplatePriceRule } from '../types'

export function findTemplateForProductType(
  templates: ProductTemplate[],
  productType: string
): ProductTemplate | undefined {
  const trimmed = productType.trim()
  if (!trimmed) return undefined
  return templates.find((template) => template.product_type === trimmed)
}

// Template model_scope is a JSON array of exact model IDs; a malformed or
// empty value means the template does not restrict the selectable models.
export function parseTemplateModelScope(
  template: ProductTemplate | null | undefined
): string[] {
  const raw = template?.model_scope?.trim()
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is string => typeof item === 'string')
  } catch {
    return []
  }
}

export function parseTemplatePriceRules(
  template: ProductTemplate | null | undefined
): ProductTemplatePriceRule[] {
  const raw = template?.price_rules?.trim()
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (item): item is ProductTemplatePriceRule =>
        typeof item === 'object' &&
        item !== null &&
        typeof (item as ProductTemplatePriceRule).price_amount === 'number'
    )
  } catch {
    return []
  }
}

// The model_scope MultiSelect may only offer models the template allows; an
// empty template scope means every enabled model stays selectable.
export function filterModelScopeOptions(
  enabledModels: string[],
  template: ProductTemplate | null | undefined
): string[] {
  const scope = parseTemplateModelScope(template)
  if (scope.length === 0) return enabledModels
  const allowed = new Set(scope)
  return enabledModels.filter((modelName) => allowed.has(modelName))
}

// "¥19.9 / ¥49.9" style summary of the template's sellable price points,
// deduplicated and sorted for stable display.
export function formatTemplatePriceRules(
  template: ProductTemplate | null | undefined
): string {
  const rules = parseTemplatePriceRules(template)
  if (rules.length === 0) return ''
  const currency = template?.currency === 'CNY' ? '¥' : '$'
  const prices = [...new Set(rules.map((rule) => rule.price_amount))].sort(
    (a, b) => a - b
  )
  return prices.map((price) => `${currency}${price}`).join(' / ')
}
