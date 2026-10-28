/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type {
  StorefrontBadge,
  StorefrontCatalogItem,
  StorefrontDisplayMetadata,
} from '../types'

const BADGE_TONES = new Set(['accent', 'beta', 'neutral', 'offer'])

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function asNonEmptyString(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

function normalizeBadges(value: unknown): StorefrontBadge[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((badge) => {
    if (!isRecord(badge)) return []
    const label = asNonEmptyString(badge.label)
    if (!label) return []
    const tone = asNonEmptyString(badge.tone)
    return [{ label, tone: tone && BADGE_TONES.has(tone) ? tone : 'neutral' }]
  })
}

function normalizeBenefits(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((benefit) => {
    const text = asNonEmptyString(benefit)
    return text ? [text] : []
  })
}

/** Only site-relative image paths may render; anything else is dropped. */
export function sanitizeStorefrontImagePath(value: unknown): string {
  const path = asNonEmptyString(value)
  if (!path || !path.startsWith('/') || path.startsWith('//')) return ''
  if (/[\s"'<>]/.test(path)) return ''
  return path
}

export function normalizeDisplayMetadata(
  value: unknown
): StorefrontDisplayMetadata {
  const record = isRecord(value) ? value : {}
  return {
    badges: normalizeBadges(record.badges),
    benefits: normalizeBenefits(record.benefits),
    button_label: asNonEmptyString(record.button_label),
    recommended: record.recommended === true,
    featured: record.featured === true,
    reference_price: asNonEmptyString(record.reference_price),
    models_display: asNonEmptyString(record.models_display),
    zone: asNonEmptyString(record.zone),
    image: sanitizeStorefrontImagePath(record.image),
    image_alt: asNonEmptyString(record.image_alt),
    preview: record.preview === true,
  }
}

/** Parse the catalog response defensively; malformed entries are dropped. */
export function normalizeStorefrontCatalog(
  value: unknown
): StorefrontCatalogItem[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((entry) => {
    if (!isRecord(entry)) return []
    const productId = asNonEmptyString(entry.product_id)
    const title = asNonEmptyString(entry.title)
    if (
      !productId ||
      !title ||
      typeof entry.price_amount !== 'number' ||
      !Number.isFinite(entry.price_amount) ||
      typeof entry.total_amount !== 'number'
    ) {
      return []
    }
    return [
      {
        product_id: productId,
        title,
        subtitle: typeof entry.subtitle === 'string' ? entry.subtitle : '',
        price_amount: entry.price_amount,
        currency: asNonEmptyString(entry.currency) ?? 'CNY',
        duration_unit: asNonEmptyString(entry.duration_unit) ?? 'month',
        duration_value:
          typeof entry.duration_value === 'number' ? entry.duration_value : 1,
        total_amount: entry.total_amount,
        custom_seconds:
          typeof entry.custom_seconds === 'number' ? entry.custom_seconds : 0,
        max_purchase_per_user:
          typeof entry.max_purchase_per_user === 'number'
            ? entry.max_purchase_per_user
            : 0,
        quota_reset_period:
          asNonEmptyString(entry.quota_reset_period) ?? 'never',
        quota_reset_custom_seconds:
          typeof entry.quota_reset_custom_seconds === 'number'
            ? entry.quota_reset_custom_seconds
            : 0,
        category: asNonEmptyString(entry.category) ?? 'package',
        sort_order: typeof entry.sort_order === 'number' ? entry.sort_order : 0,
        model_scope:
          typeof entry.model_scope === 'string' ? entry.model_scope : '',
        enabled: entry.enabled === true,
        preview: entry.preview === true,
        display_metadata: normalizeDisplayMetadata(entry.display_metadata),
      } satisfies StorefrontCatalogItem,
    ]
  })
}

/** Checkout is only offered for enabled, non-preview products. */
export function isStorefrontItemPurchasable(
  item: Pick<StorefrontCatalogItem, 'enabled' | 'preview'>
): boolean {
  return item.enabled && !item.preview
}

export function storefrontItemsForMount(
  items: StorefrontCatalogItem[],
  kind: 'plans' | 'packs' | 'zones',
  zone?: string
): StorefrontCatalogItem[] {
  if (kind === 'plans') {
    return items.filter((item) => item.category === 'subscription')
  }
  if (kind === 'packs') {
    return items.filter(
      (item) => item.category === 'package' && !item.display_metadata.zone
    )
  }
  return items.filter((item) => {
    const itemZone = item.display_metadata.zone
    if (!itemZone) return false
    return zone ? itemZone === zone : true
  })
}

export function formatStorefrontPrice(
  amount: number,
  currency: string
): string {
  const symbol = currency.toUpperCase() === 'USD' ? '$' : '¥'
  const rounded = Math.round(amount * 100) / 100
  return `${symbol}${rounded}`
}

const DURATION_UNIT_LABELS: Record<string, string> = {
  year: '年',
  month: '个月',
  day: '天',
  hour: '小时',
}

/** "30 天" style validity copy, matching the traffic-packs page convention. */
export function formatStorefrontValidity(
  durationUnit: string,
  durationValue: number
): string {
  const unit = DURATION_UNIT_LABELS[durationUnit]
  if (!unit || durationValue <= 0) return ''
  return `${durationValue} ${unit}`
}

/** "/ 30 天" style period suffix for subscription prices. */
export function formatStorefrontPeriod(
  durationUnit: string,
  durationValue: number
): string {
  const validity = formatStorefrontValidity(durationUnit, durationValue)
  return validity ? `/ ${validity}` : ''
}
