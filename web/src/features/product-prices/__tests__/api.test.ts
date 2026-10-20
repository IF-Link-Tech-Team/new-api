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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  createProductPrice,
  getAdminProductPrices,
  setProductPriceEnabled,
} from '../api'
import type { ProductPricePayload } from '../types'

afterEach(() => vi.restoreAllMocks())

describe('product price API failures', () => {
  it('rejects a failed query rather than presenting an empty price list', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: false, message: 'Price catalog unavailable' },
    })
    await expect(getAdminProductPrices()).rejects.toThrow(
      'Price catalog unavailable'
    )
  })

  it('preserves the server rejection when a new version fails validation', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: false, message: 'Cost exceeds ceiling' },
    })
    await expect(
      createProductPrice({ price: {} } as ProductPricePayload)
    ).rejects.toThrow('Cost exceeds ceiling')
  })

  it('rejects an unsuccessful sale-switch change', async () => {
    vi.spyOn(api, 'patch').mockResolvedValue({
      data: { success: false, message: 'Permission denied' },
    })
    await expect(setProductPriceEnabled(7, false)).rejects.toThrow(
      'Permission denied'
    )
  })
})
