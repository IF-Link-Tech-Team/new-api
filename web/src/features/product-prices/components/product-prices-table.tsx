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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'

import { getAdminProductPrices } from '../api'
import { useProductPricesColumns } from './product-prices-columns'
import { useProductPrices } from './product-prices-provider'

export function ProductPricesTable() {
  const { t } = useTranslation()
  const columns = useProductPricesColumns()
  const { refreshTrigger } = useProductPrices()

  const { data, isLoading } = useQuery({
    queryKey: ['admin-product-prices', refreshTrigger],
    queryFn: async () => {
      const result = await getAdminProductPrices()
      return result.data || []
    },
    placeholderData: (prev) => prev,
  })

  const prices = useMemo(() => data || [], [data])

  const { table } = useDataTable({
    data: prices,
    columns,
    withFilteredRowModel: false,
    withFacetedRowModel: false,
  })

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      emptyTitle={t('No product prices yet')}
      emptyDescription={t(
        'Click "Add Price Version" to create the first price version'
      )}
      skeletonKeyPrefix='product-prices-skeleton'
      applyHeaderSize
    />
  )
}
