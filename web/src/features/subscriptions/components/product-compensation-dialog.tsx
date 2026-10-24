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
import { zodResolver } from '@hookform/resolvers/zod'
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getProductCompensationOrders,
  retryProductCompensation,
  type ProductCompensationOrder,
} from '../api'
import {
  compensationDecisionSchema,
  type CompensationDecision,
} from '../lib/product-compensation'

export function ProductCompensationDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const canIssue = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<ProductCompensationOrder | null>(null)
  const form = useForm<CompensationDecision>({
    resolver: zodResolver(compensationDecisionSchema),
    defaultValues: { note: '' },
  })
  const orders = useInfiniteQuery({
    queryKey: ['product-compensation-orders'],
    queryFn: ({ pageParam }) => getProductCompensationOrders(pageParam),
    initialPageParam: 0,
    getNextPageParam: (page) => page.next_id || undefined,
    enabled: props.open,
  })
  const retry = useMutation({
    mutationFn: (decision: { tradeNo: string; note: string }) =>
      retryProductCompensation(decision.tradeNo, decision.note),
    onSuccess: async () => {
      setTarget(null)
      toast.success(t('Product compensation issued'))
      await queryClient.invalidateQueries({
        queryKey: ['product-compensation-orders'],
      })
    },
    onError: (error) => handleServerError(error),
  })
  const columns: StaticDataTableColumn<ProductCompensationOrder>[] = [
    {
      id: 'order',
      header: t('Order'),
      className: 'w-64',
      cell: (order) => (
        <div className='break-all whitespace-normal'>
          <div>{order.plan_title}</div>
          <div>{order.trade_no}</div>
          <div>
            {t('User ID')}: {order.user_id}
          </div>
        </div>
      ),
    },
    {
      id: 'receipt',
      header: t('Verified payment'),
      className: 'w-56',
      cell: (order) => (
        <div className='break-all whitespace-normal'>
          {/* Keep the receipt's own currency; display-currency helpers convert it. */}
          <div>
            {new Intl.NumberFormat(locale, {
              style: 'currency',
              currency: order.paid_currency,
              currencyDisplay: 'code',
            }).format(order.paid_money)}
          </div>
          <div>{order.payment_provider}</div>
          <div>{order.provider_trade_no}</div>
        </div>
      ),
    },
    {
      id: 'reason',
      header: t('Reason'),
      className: 'w-56',
      cell: (order) => (
        <div className='whitespace-normal'>
          {order.provisioning_error === 'reservation_released'
            ? t('Purchase reservation released')
            : t('Product provisioning failed')}
        </div>
      ),
    },
    {
      id: 'action',
      header: t('Actions'),
      className: 'w-36',
      cell: (order) => (
        <Button
          size='sm'
          variant='outline'
          disabled={!canIssue || retry.isPending}
          onClick={() => {
            form.reset({ note: '' })
            retry.reset()
            setTarget(order)
          }}
        >
          {t('Retry issuance')}
        </Button>
      ),
    },
  ]
  const rows = orders.data?.pages.flatMap((page) => page.items) || []
  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={(open) => {
          if (!retry.isPending) props.onOpenChange(open)
        }}
        title={t('Payments awaiting compensation')}
        description={t(
          'Verified payments without delivered entitlements. Review the receipt before issuing the original order; this does not refund money.'
        )}
        contentClassName='sm:max-w-5xl'
        footer={
          <Button
            variant='outline'
            onClick={() => void orders.refetch()}
            disabled={orders.isFetching}
          >
            {t('Refresh')}
          </Button>
        }
      >
        {!canIssue && (
          <p>{t('Only a super administrator can issue compensation')}</p>
        )}
        {orders.isPending && <LoadingState />}
        {orders.isError && <ErrorState onRetry={() => void orders.refetch()} />}
        {orders.isSuccess && rows.length === 0 && (
          <EmptyState title={t('No payments awaiting compensation')} />
        )}
        {rows.length > 0 && (
          <StaticDataTable
            tableClassName='min-w-3xl'
            columns={columns}
            data={rows}
            getRowKey={(order) => order.id}
          />
        )}
        {orders.hasNextPage && (
          <Button
            variant='outline'
            disabled={orders.isFetchingNextPage}
            onClick={() => void orders.fetchNextPage()}
          >
            {t('Load more')}
          </Button>
        )}
      </Dialog>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => {
          if (!open && !retry.isPending) setTarget(null)
        }}
        title={t('Retry issuance')}
        desc={
          <div className='break-all'>
            {target?.plan_title}
            <br />
            {target?.trade_no}
            <p>
              {t(
                'Issue the original quota, models and price version. Newer reservations and purchase limits remain protected. No cash refund is performed.'
              )}
            </p>
          </div>
        }
        confirmText={t('Confirm issuance')}
        isLoading={retry.isPending}
        disabled={!canIssue}
        handleConfirm={() =>
          void form.handleSubmit((decision) => {
            if (target) {
              retry.mutate({ tradeNo: target.trade_no, note: decision.note })
            }
          })()
        }
      >
        <Form {...form}>
          <form onSubmit={(event) => event.preventDefault()}>
            <FormField
              control={form.control}
              name='note'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Compensation reason')}</FormLabel>
                  <FormControl>
                    <Textarea
                      {...field}
                      maxLength={512}
                      disabled={retry.isPending}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </form>
        </Form>
      </ConfirmDialog>
    </>
  )
}
