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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  ApiResponse,
  ProductPrice,
  ProductPriceFilters,
  ProductPricePayload,
  ProductPriceBookVersion,
  ProductPriceBookPage,
} from './types'

export async function getProductPriceBookVersions(
  planId: number,
  beforeId = 0
): Promise<ApiResponse<ProductPriceBookPage>> {
  const params = new URLSearchParams({ limit: '20' })
  if (beforeId > 0) params.set('before_id', String(beforeId))
  const res = await api.get(
    `/api/subscription/admin/plans/${planId}/prices?${params}`
  )
  return requireServerSuccess(res.data)
}

export async function createProductPriceBookVersion(
  planId: number,
  data: ProductPricePayload
): Promise<ApiResponse<ProductPriceBookVersion>> {
  const { product_type: _legacyType, ...price } = data.price
  const res = await api.post(`/api/subscription/admin/plans/${planId}/prices`, {
    price,
  })
  return requireServerSuccess(res.data)
}

export async function setProductPriceBookVersionEnabled(
  planId: number,
  priceId: number,
  enabled: boolean,
  expectedRevision: number
): Promise<
  ApiResponse<{ price: ProductPriceBookVersion; config_revision: number }>
> {
  const res = await api.patch(
    `/api/subscription/admin/plans/${planId}/prices/${priceId}`,
    { enabled, expected_revision: expectedRevision }
  )
  return requireServerSuccess(res.data)
}

export async function getAdminProductPrices(
  filters?: ProductPriceFilters
): Promise<ApiResponse<ProductPrice[]>> {
  const params = new URLSearchParams()
  if (filters?.product_type) params.set('product_type', filters.product_type)
  if (filters?.model) params.set('model', filters.model)
  if (filters?.price_version) {
    params.set('price_version', filters.price_version)
  }
  const query = params.toString()
  const res = await api.get(
    `/api/subscription/admin/product-prices${query ? `?${query}` : ''}`
  )
  return requireServerSuccess(res.data)
}

export async function createProductPrice(
  data: ProductPricePayload
): Promise<ApiResponse<ProductPrice>> {
  const res = await api.post('/api/subscription/admin/product-prices', data)
  return requireServerSuccess(res.data)
}

// The only permitted mutation on an existing price row: the sale switch.
export async function setProductPriceEnabled(
  id: number,
  enabled: boolean
): Promise<ApiResponse> {
  const res = await api.patch(`/api/subscription/admin/product-prices/${id}`, {
    enabled,
  })
  return requireServerSuccess(res.data)
}
