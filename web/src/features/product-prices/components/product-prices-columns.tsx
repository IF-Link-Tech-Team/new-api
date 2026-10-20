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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'

import { formatEffectiveAt, formatPriceComponents } from '../lib'
import type { ProductPrice } from '../types'
import { DataTableRowActions } from './data-table-row-actions'

export function useProductPricesColumns(): ColumnDef<ProductPrice>[] {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<ProductPrice>[] => [
      {
        accessorFn: (row) => row.id,
        id: 'id',
        header: t('ID'),
        meta: { mobileHidden: true },
        cell: ({ row }) => <TableId value={row.original.id} />,
        size: 60,
      },
      {
        accessorFn: (row) => row.product_type,
        id: 'product_type',
        header: t('Product Type'),
        meta: { mobileTitle: true },
        cell: ({ row }) => (
          <span className='font-medium'>{row.original.product_type}</span>
        ),
        size: 180,
      },
      {
        accessorFn: (row) => row.model,
        id: 'model',
        header: t('Model'),
        cell: ({ row }) => (
          <span className='text-muted-foreground'>{row.original.model}</span>
        ),
        size: 200,
      },
      {
        accessorFn: (row) => row.price_version,
        id: 'price_version',
        header: t('Price Version'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <StatusBadge
            label={row.original.price_version}
            variant='neutral'
            copyable={false}
          />
        ),
        size: 140,
      },
      {
        id: 'selling_price',
        header: t('Selling Price (in / cached / out)'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <span className='font-mono text-xs'>
            {formatPriceComponents(row.original.user_selling_price)}
          </span>
        ),
        size: 180,
      },
      {
        id: 'max_upstream_cost',
        header: t('Max Upstream Cost'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <span className='text-muted-foreground font-mono text-xs'>
            {formatPriceComponents(row.original.max_upstream_cost)}
          </span>
        ),
        size: 150,
      },
      {
        accessorFn: (row) => row.billing_multiplier,
        id: 'billing_multiplier',
        header: t('Multiplier'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <span className='text-muted-foreground'>
            {row.original.billing_multiplier}
          </span>
        ),
        size: 90,
      },
      {
        accessorFn: (row) => row.effective_at,
        id: 'effective_at',
        header: t('Effective At'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <span className='text-muted-foreground'>
            {formatEffectiveAt(row.original.effective_at)}
          </span>
        ),
        size: 140,
      },
      {
        accessorFn: (row) => row.enabled,
        id: 'enabled',
        header: t('Status'),
        meta: { mobileBadge: true },
        cell: ({ row }) =>
          row.original.enabled ? (
            <StatusBadge
              label={t('Enable')}
              variant='success'
              copyable={false}
              className='-ml-1.5'
            />
          ) : (
            <StatusBadge
              label={t('Disable')}
              variant='neutral'
              copyable={false}
              className='-ml-1.5'
            />
          ),
        size: 80,
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <DataTableRowActions row={row} />,
        meta: { pinned: 'right' as const },
      },
    ],
    [t]
  )
}
