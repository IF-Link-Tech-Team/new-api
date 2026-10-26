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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  channelPriceRows,
  getAPIChannelCatalog,
} from '@/features/api-channel-prices/api'
import { getProductPriceBookVersions } from '@/features/product-prices/api'
import type { ProductPriceBookVersion } from '@/features/product-prices/types'
import { splitModelScope } from '@/features/subscriptions/lib/plan-form'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { stageProductChannelVersion } from '../api'
import type { SubscriptionPlan } from '../types'

type RateDraft = {
  enabled: boolean
  multiplier: string
  position: string
}

type Props = {
  product: SubscriptionPlan
  revision: number
  versions: ProductPriceBookVersion[]
  onClose: () => void
  onRevisionChange: (revision: number) => void
  onVersionStaged: (version: string) => void
}

export function ProductChannelVersionDrawer(props: Props) {
  const { t } = useTranslation()
  const catalog = useQuery({
    queryKey: ['api-channel-prices'],
    queryFn: getAPIChannelCatalog,
  })
  const [version, setVersion] = useState('')
  const [reason, setReason] = useState('')
  const [drafts, setDrafts] = useState<Record<string, RateDraft>>({})
  const [fallbackAllowed, setFallbackAllowed] = useState(
    props.product.fallback_allowed ?? true
  )
  const models = useMemo(
    () => splitModelScope(props.product.model_scope || ''),
    [props.product.model_scope]
  )
  const available = useMemo(
    () =>
      catalog.data
        ? channelPriceRows(catalog.data).filter(
            (row) =>
              models.includes(row.model) &&
              row.price?.enabled &&
              row.mapping_valid &&
              row.channel_status === 1
          )
        : [],
    [catalog.data, models]
  )
  const save = useMutation({
    mutationFn: async () => {
      if (!catalog.data) {
        throw new Error(t('API channel catalog is unavailable'))
      }
      const grouped = new Map<
        string,
        { channel_id: number; multiplier: number; position: number | null }[]
      >()
      for (const row of available) {
        const draft = drafts[`${row.model}:${row.channel_id}`]
        if (!draft?.enabled) continue
        const multiplier = Number(draft.multiplier)
        const position =
          draft.position.trim() === '' ? null : Number(draft.position)
        if (
          !Number.isFinite(multiplier) ||
          multiplier <= 0 ||
          multiplier > 99.999999
        ) {
          throw new Error(
            t('Multiplier must be greater than 0 and at most 99.999999')
          )
        }
        if (
          position !== null &&
          (!Number.isInteger(position) || position < 0 || position > 10000)
        ) {
          throw new Error(
            t('Route position must be an integer from 0 to 10000')
          )
        }
        const rates = grouped.get(row.model) || []
        rates.push({ channel_id: row.channel_id, multiplier, position })
        grouped.set(row.model, rates)
      }
      const configured = models.map((model) => ({
        model,
        rates: grouped.get(model) || [],
      }))
      if (configured.some((item) => item.rates.length === 0)) {
        throw new Error(
          t('Select at least one active API channel for every product model')
        )
      }
      const response = await stageProductChannelVersion(props.product.id, {
        expected_revision: props.revision,
        expected_api_revision: catalog.data.revision,
        version: version.trim(),
        fallback_allowed: fallbackAllowed,
        models: configured,
        reason: reason.trim(),
      })
      const data = requireServerSuccess(response).data
      if (!data) throw new Error(t('Invalid server response'))
      return data
    },
    onSuccess: (data) => {
      props.onRevisionChange(data.config_revision)
      props.onVersionStaged(version.trim())
      props.onClose()
    },
    onError: (error) => handleServerError(error),
  })

  const copyPrevious = useMutation({
    mutationFn: async () => {
      const copied: Record<string, RateDraft> = {}
      const sourceVersion =
        props.product.price_version ||
        props.versions.find((item) => item.channel_rates)?.price_version
      if (!sourceVersion) {
        throw new Error(t('No previous channel version is available'))
      }
      const previousRows: ProductPriceBookVersion[] = []
      let cursor = 0
      do {
        const response = await getProductPriceBookVersions(
          props.product.id,
          cursor,
          sourceVersion
        )
        const data = requireServerSuccess(response).data
        if (!data) throw new Error(t('Invalid server response'))
        previousRows.push(...data.items)
        cursor = data.next_id
      } while (cursor)
      for (const model of models) {
        const previous = previousRows.find(
          (item) =>
            item.model === model &&
            item.price_version === sourceVersion &&
            item.channel_rates
        )
        if (!previous?.channel_rates) {
          throw new Error(
            t(
              'The previous version has no channel rates for a selected model. Configure that model manually.'
            )
          )
        }
        const rates = JSON.parse(previous.channel_rates) as {
          channel_id: number
          multiplier: number
          position: number | null
        }[]
        for (const rate of rates) {
          if (
            !available.some(
              (row) => row.model === model && row.channel_id === rate.channel_id
            )
          ) {
            throw new Error(
              t(
                'The previous version contains an unavailable API channel. Review it before copying.'
              )
            )
          }
          copied[`${model}:${rate.channel_id}`] = {
            enabled: true,
            multiplier: String(rate.multiplier),
            position: rate.position === null ? '' : String(rate.position),
          }
        }
      }
      return {
        copied,
        fallback:
          previousRows.find((row) => row.channel_rates)?.fallback_allowed ??
          true,
      }
    },
    onSuccess: (result) => {
      setDrafts(result.copied)
      setFallbackAllowed(result.fallback)
    },
    onError: (error) => handleServerError(error),
  })

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending && !copyPrevious.isPending) props.onClose()
      }}
      title={t('Configure product channel rates')}
      description={props.product.title}
      contentHeight='min(75dvh, 760px)'
      footer={
        <>
          <Button
            variant='outline'
            disabled={save.isPending || copyPrevious.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            disabled={
              save.isPending ||
              copyPrevious.isPending ||
              catalog.isLoading ||
              !version.trim() ||
              !reason.trim()
            }
            onClick={() => save.mutate()}
          >
            {t('Save draft version')}
          </Button>
        </>
      }
    >
      <div className='flex flex-col gap-4'>
        <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
          <Field>
            <FieldLabel htmlFor='product-channel-version'>
              {t('Price Version')}
            </FieldLabel>
            <Input
              id='product-channel-version'
              disabled={save.isPending || copyPrevious.isPending}
              value={version}
              onChange={(event) => setVersion(event.target.value)}
              maxLength={64}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='product-channel-reason'>
              {t('Reason for change')}
            </FieldLabel>
            <Input
              id='product-channel-reason'
              disabled={save.isPending || copyPrevious.isPending}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              maxLength={2000}
            />
          </Field>
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Only enabled API model and channel pairs are shown. New drafts do not affect current sales until the product version is saved.'
          )}
        </p>
        <Button
          type='button'
          variant='outline'
          disabled={save.isPending || copyPrevious.isPending || !catalog.data}
          onClick={() => copyPrevious.mutate()}
        >
          {t('Copy previous product version')}
        </Button>
        <Field orientation='horizontal'>
          <Switch
            id='product-channel-fallback'
            checked={fallbackAllowed}
            onCheckedChange={setFallbackAllowed}
            disabled={save.isPending || copyPrevious.isPending}
          />
          <FieldLabel htmlFor='product-channel-fallback'>
            {t('Allow route fallback')}
          </FieldLabel>
        </Field>
        {catalog.isPending && <LoadingState size='sm' />}
        {catalog.isError && (
          <ErrorState onRetry={() => void catalog.refetch()} />
        )}
        {models.map((model) => (
          <FieldSet
            key={model}
            disabled={save.isPending || copyPrevious.isPending}
            className='min-w-0 rounded-lg border p-3'
          >
            <FieldLegend className='max-w-full break-all'>{model}</FieldLegend>
            {available
              .filter((row) => row.model === model)
              .map((row) => {
                const key = `${row.model}:${row.channel_id}`
                const draft = drafts[key] || {
                  enabled: false,
                  multiplier: '1',
                  position: '',
                }
                return (
                  <FieldGroup
                    key={key}
                    className='grid grid-cols-1 items-end gap-3 sm:grid-cols-[minmax(0,1fr)_auto_6rem_6rem]'
                  >
                    <div className='min-w-0'>
                      <p className='break-all'>
                        {row.channel_name} · #{row.channel_id}
                      </p>
                      <p className='text-muted-foreground text-xs break-all'>
                        {row.upstream_model}
                      </p>
                    </div>
                    <Field orientation='horizontal'>
                      <Switch
                        id={`allow-${key}`}
                        checked={draft.enabled}
                        onCheckedChange={(enabled) =>
                          setDrafts((current) => ({
                            ...current,
                            [key]: { ...draft, enabled },
                          }))
                        }
                      />
                      <FieldLabel htmlFor={`allow-${key}`}>
                        {t('Allowed')}
                      </FieldLabel>
                    </Field>
                    <Field data-disabled={!draft.enabled}>
                      <FieldLabel htmlFor={`multiplier-${key}`}>
                        {t('Multiplier')}
                      </FieldLabel>
                      <Input
                        id={`multiplier-${key}`}
                        inputMode='decimal'
                        value={draft.multiplier}
                        disabled={!draft.enabled}
                        onChange={(event) =>
                          setDrafts((current) => ({
                            ...current,
                            [key]: { ...draft, multiplier: event.target.value },
                          }))
                        }
                      />
                    </Field>
                    <Field data-disabled={!draft.enabled}>
                      <FieldLabel htmlFor={`position-${key}`}>
                        {t('Route position')}
                      </FieldLabel>
                      <Input
                        id={`position-${key}`}
                        inputMode='numeric'
                        placeholder={t('Inherit')}
                        value={draft.position}
                        disabled={!draft.enabled}
                        onChange={(event) =>
                          setDrafts((current) => ({
                            ...current,
                            [key]: { ...draft, position: event.target.value },
                          }))
                        }
                      />
                    </Field>
                  </FieldGroup>
                )
              })}
            {catalog.data && !available.some((row) => row.model === model) && (
              <p role='alert' className='text-destructive text-sm'>
                {t('No active API channel price is configured for this model')}
              </p>
            )}
          </FieldSet>
        ))}
      </div>
    </Dialog>
  )
}
