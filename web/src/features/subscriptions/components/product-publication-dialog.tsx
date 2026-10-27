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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getProductPublications,
  previewProductPublication,
  publishProductAndSync,
  type ProductPublicationPreview,
  type ProductPublicationRequest,
  type ProductSyncField,
} from '../api'
import type { PlanPayload, SubscriptionPlan } from '../types'

export function ProductPublicationDialog(props: {
  planID: number
  payload: PlanPayload
  beforePlan: SubscriptionPlan
  onClose: () => void
  onPublished: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [fields, setFields] = useState<ProductSyncField[]>(() => {
    const before = props.beforePlan
    const after = props.payload.plan
    const changed: ProductSyncField[] = []
    if (
      before.price_version !== after.price_version ||
      before.model_scope !== after.model_scope ||
      before.fallback_allowed !== after.fallback_allowed
    ) {
      changed.push('pricing')
    }
    if (before.total_amount !== after.total_amount) changed.push('quota')
    if (
      before.duration_unit !== after.duration_unit ||
      before.duration_value !== after.duration_value ||
      (before.custom_seconds ?? 0) !== (after.custom_seconds ?? 0)
    ) {
      changed.push('expiry')
    }
    if (
      before.quota_reset_period !== after.quota_reset_period ||
      (before.quota_reset_custom_seconds ?? 0) !==
        (after.quota_reset_custom_seconds ?? 0)
    ) {
      changed.push('reset')
    }
    return changed
  })
  const [reason, setReason] = useState('')
  const [requestKey] = useState(() => crypto.randomUUID())
  const [preview, setPreview] = useState<ProductPublicationPreview | null>(null)
  const labels: Record<ProductSyncField, string> = {
    pricing: t('Models, channel rates and routing'),
    quota: t('Total entitlement quota'),
    expiry: t('Expiry from original purchase date'),
    reset: t('Quota reset schedule'),
  }
  const request: ProductPublicationRequest = {
    ...props.payload,
    fields,
    reason,
    request_key: requestKey,
  }
  const previewMutation = useMutation({
    mutationFn: () => previewProductPublication(props.planID, request),
    onSuccess: setPreview,
    onError: handleServerError,
  })
  const publish = useMutation({
    mutationFn: () =>
      publishProductAndSync(props.planID, {
        ...request,
        preview_hash: preview?.hash,
      }),
    onSuccess: props.onPublished,
    onError: (error) => {
      setPreview(null)
      handleServerError(error)
    },
  })
  const busy = previewMutation.isPending || publish.isPending

  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
      title={t('Publish product and sync entitlements')}
      desc={t(
        'Select the fields to synchronize to active entitlements. Used quota and historical orders are preserved.'
      )}
      className='max-h-[90dvh] overflow-y-auto data-[size=default]:sm:max-w-4xl'
      isLoading={busy}
      disabled={!reason.trim() || !!preview?.blocked}
      confirmText={preview ? t('Publish and synchronize') : t('Preview impact')}
      handleConfirm={() => {
        if (preview) publish.mutate(undefined)
        else previewMutation.mutate(undefined)
      }}
    >
      <div className='grid gap-3 sm:grid-cols-2'>
        {(Object.keys(labels) as ProductSyncField[]).map((field) => (
          <label key={field} className='flex items-center gap-2 text-sm'>
            <Checkbox
              checked={fields.includes(field)}
              disabled={busy}
              onCheckedChange={(checked) => {
                setFields((previous) =>
                  checked
                    ? [...previous, field]
                    : previous.filter((value) => value !== field)
                )
                setPreview(null)
              }}
            />
            {labels[field]}
          </label>
        ))}
      </div>
      <label className='grid gap-2 text-sm'>
        {t('Reason')}
        <Input
          value={reason}
          maxLength={2000}
          disabled={busy}
          onChange={(event) => {
            setReason(event.target.value)
            setPreview(null)
          }}
        />
      </label>
      {preview && (
        <div className='min-w-0 space-y-3'>
          <p>
            {t('Affected entitlements')}:{' '}
            {formatNumber(preview.affected, locale)} · {t('Blocked')}:{' '}
            {formatNumber(preview.blocked, locale)}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Showing up to 100 entitlement changes. Asset changes wait for in-flight requests to settle.'
            )}
          </p>
          <StaticDataTable
            data={preview.changes}
            getRowKey={(row) => row.before.id}
            columns={[
              {
                id: 'user',
                header: t('User ID'),
                cell: (row) => (
                  <div>
                    #{row.before.user_id}
                    <div className='text-muted-foreground text-xs'>
                      #{row.before.id}
                    </div>
                  </div>
                ),
              },
              {
                id: 'changes',
                header: t('Changes'),
                cell: (row) => (
                  <div className='space-y-1 text-sm break-all whitespace-normal'>
                    {fields.includes('pricing') && (
                      <>
                        <div>
                          {row.before.price_version} → {row.after.price_version}
                        </div>
                        <div>
                          {row.before.model_scope} → {row.after.model_scope}
                        </div>
                      </>
                    )}
                    {fields.includes('quota') && (
                      <div>
                        {t('Total entitlement quota')}:{' '}
                        <span className='whitespace-nowrap'>
                          {formatNumber(row.before.amount_total, locale)} →{' '}
                          {formatNumber(row.after.amount_total, locale)}
                        </span>{' '}
                        ({t('Used')}:{' '}
                        <span className='whitespace-nowrap'>
                          {formatNumber(row.before.amount_used, locale)}
                        </span>
                        )
                      </div>
                    )}
                    {fields.includes('expiry') && (
                      <div>
                        {t('Expiry time')}:{' '}
                        {new Date(row.before.end_time * 1000).toLocaleString(
                          locale
                        )}{' '}
                        →{' '}
                        {new Date(row.after.end_time * 1000).toLocaleString(
                          locale
                        )}
                      </div>
                    )}
                    {fields.includes('reset') && (
                      <div>
                        {t('Quota reset schedule')}: {row.before.reset_period} →{' '}
                        {row.after.reset_period} ({row.after.reset_seconds}s)
                      </div>
                    )}
                    {row.blocker && (
                      <div className='text-destructive'>{t(row.blocker)}</div>
                    )}
                  </div>
                ),
              },
            ]}
          />
        </div>
      )}
    </ConfirmDialog>
  )
}

export function ProductPublicationHistoryDialog(props: { planID: number }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [publicationID, setPublicationID] = useState<number | undefined>()
  const [afterID, setAfterID] = useState(0)
  const history = useQuery({
    queryKey: ['product-publications', props.planID, publicationID, afterID],
    queryFn: () => getProductPublications(props.planID, publicationID, afterID),
    enabled: open,
    refetchInterval: (query) =>
      query.state.data?.counts.some((count) =>
        ['pending', 'retry', 'cache_pending'].includes(count.status)
      )
        ? 5000
        : false,
  })
  return (
    <>
      <Button type='button' variant='outline' onClick={() => setOpen(true)}>
        {t('Publication history')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t('Publication history')}
        contentClassName='sm:max-w-4xl'
        contentHeight='70dvh'
      >
        <div className='space-y-4'>
          {history.isError && (
            <Button onClick={() => history.refetch()}>{t('Retry')}</Button>
          )}
          {history.data?.publications.map((publication) => (
            <div key={publication.id} className='space-y-1 rounded border p-3'>
              <Button
                variant='link'
                onClick={() => {
                  setPublicationID(publication.id)
                  setAfterID(0)
                }}
              >
                #{publication.id} · {t('Version')} {publication.revision}
              </Button>
              <p className='text-sm break-all'>{publication.reason}</p>
              <p className='text-muted-foreground text-sm'>
                {history.data.counts
                  .filter((count) => count.publication_id === publication.id)
                  .map((count) => `${t(count.status)}: ${count.count}`)
                  .join(' · ')}
              </p>
            </div>
          ))}
          {publicationID && (
            <StaticDataTable
              data={history.data?.items ?? []}
              getRowKey={(item) => item.id}
              columns={[
                {
                  id: 'user',
                  header: t('User ID'),
                  cell: (item) => (
                    <div>
                      #{item.user_id} / #{item.subscription_id}
                    </div>
                  ),
                },
                {
                  id: 'status',
                  header: t('Status'),
                  cell: (item) => (
                    <div className='break-all whitespace-normal'>
                      {t(item.status)}
                      <div>{t(item.last_error)}</div>
                    </div>
                  ),
                },
              ]}
            />
          )}
          {publicationID && (
            <div className='flex gap-2'>
              <Button disabled={afterID === 0} onClick={() => setAfterID(0)}>
                {t('First page')}
              </Button>
              <Button
                disabled={(history.data?.items.length ?? 0) < 100}
                onClick={() => setAfterID(history.data?.items.at(-1)?.id ?? 0)}
              >
                {t('Next page')}
              </Button>
            </div>
          )}
        </div>
      </Dialog>
    </>
  )
}
