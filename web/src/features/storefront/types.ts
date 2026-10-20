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

export interface StorefrontBadge {
  label: string
  tone?: string
}

/** Display-only card copy carried on the plan row; purchase gates stay server-side. */
export interface StorefrontDisplayMetadata {
  badges?: StorefrontBadge[]
  benefits?: string[]
  button_label?: string
  recommended?: boolean
  featured?: boolean
  reference_price?: string
  models_display?: string
  zone?: string
  image?: string
  image_alt?: string
  preview?: boolean
}

/** Trimmed public view of a subscription plan from /api/subscription/public/catalog. */
export interface StorefrontCatalogItem {
  product_id: string
  title: string
  subtitle: string
  price_amount: number
  currency: string
  duration_unit: string
  duration_value: number
  total_amount: number
  category: string
  sort_order: number
  model_scope: string
  enabled: boolean
  preview: boolean
  display_metadata: StorefrontDisplayMetadata
}

export type StorefrontMountKind = 'plans' | 'packs' | 'zones'

export interface StorefrontMount {
  element: HTMLElement
  kind: StorefrontMountKind
  zone?: string
}
