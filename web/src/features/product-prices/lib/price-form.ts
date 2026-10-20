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

import type { PriceComponents, ProductPricePayload } from '../types'

export function getPriceFormSchema(t: TFunction) {
  return z.object({
    product_type: z.string().min(1, t('Please select a product type')),
    model: z.string().trim().min(1, t('Please enter the exact model ID')),
    price_version: z.string().trim().min(1, t('Please enter a price version')),
    effective_at: z.coerce.number().min(0),
    billing_multiplier: z.coerce.number().min(0),
    billing_expression: z
      .string()
      .trim()
      .min(1, t('Please enter a billing expression')),
    allowed_channels: z
      .string()
      .trim()
      .min(1, t('Please enter at least one channel ID')),
    official_input: z.coerce.number().min(0),
    official_cached_input: z.coerce.number().min(0),
    official_output: z.coerce.number().min(0),
    selling_input: z.coerce.number().min(0),
    selling_cached_input: z.coerce.number().min(0),
    selling_output: z.coerce.number().min(0),
    upstream_input: z.coerce.number().min(0),
    upstream_cached_input: z.coerce.number().min(0),
    upstream_output: z.coerce.number().min(0),
    cost_input: z.coerce.number().min(0),
    cost_cached_input: z.coerce.number().min(0),
    cost_output: z.coerce.number().min(0),
    fallback_allowed: z.boolean(),
    enabled: z.boolean(),
  })
}

export type PriceFormValues = z.infer<ReturnType<typeof getPriceFormSchema>>

export const PRICE_FORM_DEFAULTS: PriceFormValues = {
  product_type: '',
  model: '',
  price_version: '',
  effective_at: Math.floor(Date.now() / 1000),
  billing_multiplier: 1,
  billing_expression: '',
  allowed_channels: '',
  official_input: 0,
  official_cached_input: 0,
  official_output: 0,
  selling_input: 0,
  selling_cached_input: 0,
  selling_output: 0,
  upstream_input: 0,
  upstream_cached_input: 0,
  upstream_output: 0,
  cost_input: 0,
  cost_cached_input: 0,
  cost_output: 0,
  fallback_allowed: false,
  enabled: true,
}

// Price component fields travel as JSON strings; the form edits the three
// core components (input / cached input / output) as numbers.
export function buildPriceComponents(
  input: number,
  cachedInput: number,
  output: number
): string {
  const components: PriceComponents = {
    input,
    cached_input: cachedInput,
    output,
  }
  return JSON.stringify(components)
}

export function parsePriceComponents(raw: string): PriceComponents | null {
  const trimmed = raw.trim()
  if (!trimmed) return null
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (typeof parsed !== 'object' || parsed === null) return null
    const candidate = parsed as Record<string, unknown>
    if (
      typeof candidate.input !== 'number' ||
      typeof candidate.cached_input !== 'number' ||
      typeof candidate.output !== 'number'
    ) {
      return null
    }
    return {
      input: candidate.input,
      cached_input: candidate.cached_input,
      output: candidate.output,
      ...(typeof candidate.cache_creation === 'number'
        ? { cache_creation: candidate.cache_creation }
        : {}),
    }
  } catch {
    return null
  }
}

// Channel IDs are edited as a comma-separated list and stored as a JSON
// array; invalid or duplicate entries are rejected before submit.
export function parseChannelIds(raw: string): number[] | null {
  const parts = raw
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
  if (parts.length === 0) return null
  const ids: number[] = []
  const seen = new Set<number>()
  for (const part of parts) {
    if (!/^\d+$/.test(part)) return null
    const id = Number.parseInt(part, 10)
    if (id <= 0 || seen.has(id)) return null
    seen.add(id)
    ids.push(id)
  }
  return ids
}

export function formValuesToPricePayload(
  values: PriceFormValues
): ProductPricePayload | null {
  const channelIds = parseChannelIds(values.allowed_channels)
  if (!channelIds) return null
  return {
    price: {
      product_type: values.product_type.trim(),
      model: values.model.trim(),
      price_version: values.price_version.trim(),
      effective_at: Number(values.effective_at || 0),
      billing_multiplier: Number(values.billing_multiplier || 0),
      billing_expression: values.billing_expression.trim(),
      allowed_channels: JSON.stringify(channelIds),
      official_reference_price: buildPriceComponents(
        Number(values.official_input || 0),
        Number(values.official_cached_input || 0),
        Number(values.official_output || 0)
      ),
      user_selling_price: buildPriceComponents(
        Number(values.selling_input || 0),
        Number(values.selling_cached_input || 0),
        Number(values.selling_output || 0)
      ),
      upstream_settlement_price: buildPriceComponents(
        Number(values.upstream_input || 0),
        Number(values.upstream_cached_input || 0),
        Number(values.upstream_output || 0)
      ),
      max_upstream_cost: buildPriceComponents(
        Number(values.cost_input || 0),
        Number(values.cost_cached_input || 0),
        Number(values.cost_output || 0)
      ),
      fallback_allowed: values.fallback_allowed,
      enabled: values.enabled,
    },
  }
}
