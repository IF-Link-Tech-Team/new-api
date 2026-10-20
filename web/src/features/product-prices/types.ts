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
// Product Price Schema & Types
// ============================================================================

export const priceComponentsSchema = z.object({
  input: z.number(),
  cached_input: z.number(),
  cache_creation: z.number().optional(),
  output: z.number(),
})

export type PriceComponents = z.infer<typeof priceComponentsSchema>

// ProductPrice rows are immutable version snapshots; the backend BeforeUpdate
// hook rejects any field change, so the UI only creates new versions and
// toggles the enabled switch.
export const productPriceSchema = z.object({
  id: z.number(),
  product_type: z.string(),
  model: z.string(),
  price_version: z.string(),
  effective_at: z.number(),
  official_reference_price: z.string(),
  user_selling_price: z.string(),
  upstream_settlement_price: z.string(),
  billing_expression: z.string().optional(),
  billing_multiplier: z.number(),
  allowed_channels: z.string(),
  max_upstream_cost: z.string(),
  fallback_allowed: z.boolean(),
  enabled: z.boolean(),
  created_at: z.number().optional(),
  updated_at: z.number().optional(),
})

export type ProductPrice = z.infer<typeof productPriceSchema>

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export interface ProductPricePayload {
  price: Partial<ProductPrice>
}

export interface ProductPriceFilters {
  product_type?: string
  model?: string
  price_version?: string
}

export type ProductPricesDialogType = 'create' | 'toggle-enabled'
