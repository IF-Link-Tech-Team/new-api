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

export interface PurchaseCatalogItem {
  id: string
  name: string
  description: string
  amount: string
}

type PurchaseCategory = 'package' | 'subscription'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function normalizeItems(
  value: unknown,
  category: PurchaseCategory
): PurchaseCatalogItem[] {
  if (!Array.isArray(value)) {
    return []
  }

  return value.flatMap((entry) => {
    if (
      !isRecord(entry) ||
      typeof entry.id !== 'string' ||
      typeof entry.name !== 'string' ||
      typeof entry.description !== 'string' ||
      typeof entry.price !== 'string'
    ) {
      return []
    }

    const period =
      category === 'subscription' && typeof entry.period === 'string'
        ? entry.period
        : ''
    return [
      {
        id: entry.id,
        name: entry.name.replaceAll(/\s+/g, ' ').trim(),
        description: entry.description,
        amount: `${entry.price}${period}`,
      },
    ]
  })
}

export function purchaseCatalogFromDocument(
  value: unknown,
  category: PurchaseCategory
): PurchaseCatalogItem[] {
  if (!isRecord(value)) {
    return []
  }

  return normalizeItems(
    category === 'subscription' ? value.plans : value.traffic_packs,
    category
  )
}

export async function loadPurchaseCatalog(
  category: PurchaseCategory,
  fetcher: typeof fetch = fetch
): Promise<PurchaseCatalogItem[]> {
  try {
    const response = await fetcher('/purchase-catalog.json', {
      cache: 'no-store',
      credentials: 'same-origin',
    })
    if (!response.ok) {
      return []
    }

    return purchaseCatalogFromDocument(await response.json(), category)
  } catch {
    return []
  }
}
