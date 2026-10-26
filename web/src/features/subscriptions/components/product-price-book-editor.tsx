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
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getAPIChannelCatalog } from '@/features/api-channel-prices/api'
import {
  getProductPriceBookVersions,
  setProductPriceBookVersionEnabled,
} from '@/features/product-prices/api'
import { ProductPriceVersionDrawer } from '@/features/product-prices/components/product-prices-mutate-drawer'
import type { ProductPriceBookVersion } from '@/features/product-prices/types'

import type { SubscriptionPlan } from '../types'
import { ProductChannelVersionDrawer } from './product-channel-version-drawer'

interface Props {
  product: SubscriptionPlan
  revision: number
  disabled: boolean
  models?: string[]
  onRevisionChange: (revision: number) => void
  onVersionStaged: (version: string) => void
}

// Reuses the price-version form and confirmation/state components. The product
// is always supplied by the owning plan editor, never by a fixed product type.
export function ProductPriceBookEditor(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [channelCreating, setChannelCreating] = useState(false)
  const [target, setTarget] = useState<ProductPriceBookVersion | null>(null)
  const catalog = useQuery({
    queryKey: ['api-channel-prices'],
    queryFn: getAPIChannelCatalog,
  })
  const queryKey = ['product-price-book', props.product.id]
  const prices = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) =>
      getProductPriceBookVersions(props.product.id, pageParam),
    initialPageParam: 0,
    getNextPageParam: (page) => page.data?.next_id || undefined,
  })
  const toggle = useMutation({
    mutationFn: (price: ProductPriceBookVersion) =>
      setProductPriceBookVersionEnabled(
        props.product.id,
        price.id,
        !price.enabled,
        props.revision
      ),
    onSuccess: (res) => {
      if (res.data) props.onRevisionChange(res.data.config_revision)
      setTarget(null)
      return queryClient.invalidateQueries({ queryKey })
    },
  })
  const rows =
    prices.data?.pages.flatMap((page) => page.data?.items || []) || []
  return (
    <div className='flex min-w-0 flex-col gap-3'>
      {!catalog.data?.active && (
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled || toggle.isPending}
          onClick={() => setCreating(true)}
        >
          {t('Add Price Version')}
        </Button>
      )}
      <Button
        type='button'
        variant='outline'
        disabled={props.disabled || toggle.isPending}
        onClick={() => setChannelCreating(true)}
      >
        {t('Configure model and channel rates')}
      </Button>
      {prices.isPending && <LoadingState size='sm' />}
      {prices.isError && <ErrorState onRetry={() => void prices.refetch()} />}
      {prices.isSuccess && rows.length === 0 && (
        <EmptyState
          title={t('No product model prices yet')}
          description={t(
            'Add a price for every selected model, using the same version name.'
          )}
        />
      )}
      {rows.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Price Version')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((price) => (
              <TableRow key={price.id}>
                <TableCell className='max-w-44 break-all whitespace-normal'>
                  {price.model}
                </TableCell>
                <TableCell className='max-w-32 break-all whitespace-normal'>
                  {price.price_version}
                </TableCell>
                <TableCell>
                  <Badge variant={price.enabled ? 'secondary' : 'outline'}>
                    {price.enabled ? t('Enabled') : t('Disabled')}
                  </Badge>
                </TableCell>
                <TableCell>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    aria-label={t(
                      'Change sale switch for {{model}} {{version}}',
                      { model: price.model, version: price.price_version }
                    )}
                    disabled={props.disabled || toggle.isPending}
                    onClick={() => setTarget(price)}
                  >
                    {price.enabled ? t('Disable') : t('Enable')}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {prices.hasNextPage && (
        <Button
          type='button'
          variant='outline'
          disabled={prices.isFetchingNextPage}
          onClick={() => void prices.fetchNextPage()}
        >
          {t('Load more')}
        </Button>
      )}
      <ProductPriceVersionDrawer
        open={creating}
        onOpenChange={setCreating}
        product={props.product}
        onCreated={() => void queryClient.invalidateQueries({ queryKey })}
      />
      {channelCreating && (
        <ProductChannelVersionDrawer
          product={{
            ...props.product,
            model_scope: props.models?.join(',') ?? props.product.model_scope,
          }}
          revision={props.revision}
          versions={rows}
          onClose={() => setChannelCreating(false)}
          onRevisionChange={(revision) => {
            props.onRevisionChange(revision)
            void queryClient.invalidateQueries({ queryKey })
          }}
          onVersionStaged={props.onVersionStaged}
        />
      )}
      {target && (
        <ConfirmDialog
          open
          onOpenChange={(open) => {
            if (!open && !toggle.isPending) setTarget(null)
          }}
          title={target.enabled ? t('Confirm disable') : t('Confirm enable')}
          desc={t(
            'This changes new sales only. Paid entitlements keep their price version.'
          )}
          destructive={target.enabled}
          isLoading={toggle.isPending}
          handleConfirm={() => toggle.mutate(target)}
        />
      )}
    </div>
  )
}
