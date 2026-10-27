/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { searchChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { requireServerSuccess } from '@/lib/server-error-message'

// Channel discovery and mappings stay in the existing channel API. This
// component only chooses candidates for the current product/model version.
export function ProductChannelOrder(props: {
  model: string
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['product-route-channels', props.model],
    enabled: !!props.model,
    queryFn: async () =>
      requireServerSuccess(await searchChannels({ model: props.model })),
  })
  const channels = (query.data?.data?.items || []).filter((channel) =>
    channel.models.split(',').some((name) => name.trim() === props.model)
  )
  const ids = props.value
    .split(',')
    .map((id) => id.trim())
    .filter(Boolean)
  const update = (next: string[]) => props.onChange(next.join(', '))
  return (
    <div className='space-y-3'>
      {query.isPending && props.model && <LoadingState size='sm' />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <Combobox
        value=''
        disabled={
          props.disabled || !props.model || query.isPending || query.isError
        }
        placeholder={t('Add a channel to this model')}
        options={channels
          .filter(
            (channel) =>
              channel.status === 1 && !ids.includes(String(channel.id))
          )
          .map((channel) => ({
            value: String(channel.id),
            label: `${channel.name} (#${channel.id})`,
          }))}
        onValueChange={(id) => {
          if (id && !ids.includes(id)) update([...ids, id])
        }}
      />
      {ids.map((id, index) => (
        <div key={id} className='space-y-2 rounded-md border p-3'>
          <ChannelDetails
            channel={channels.find((channel) => String(channel.id) === id)}
            id={id}
            model={props.model}
            position={index + 1}
          />
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={props.disabled || index === 0}
              onClick={() => {
                const next = [...ids]
                ;[next[index - 1], next[index]] = [next[index], next[index - 1]]
                update(next)
              }}
            >
              {t('Move up')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={props.disabled || index === ids.length - 1}
              onClick={() => {
                const next = [...ids]
                ;[next[index], next[index + 1]] = [next[index + 1], next[index]]
                update(next)
              }}
            >
              {t('Move down')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={props.disabled}
              onClick={() =>
                update(ids.filter((candidate) => candidate !== id))
              }
            >
              {t('Remove')}
            </Button>
          </div>
        </div>
      ))}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Channel status and cost are current configuration snapshots. Saving still validates every candidate; changing this order affects this new price version only.'
        )}
      </p>
    </div>
  )
}

function ChannelDetails(props: {
  channel?: Channel
  id: string
  model: string
  position: number
}) {
  const { t } = useTranslation()
  let upstream = props.model
  let cost: unknown
  try {
    const mappings: unknown = JSON.parse(props.channel?.model_mapping || '{}')
    if (mappings && typeof mappings === 'object' && props.model in mappings) {
      const value: unknown = (mappings as Record<string, unknown>)[props.model]
      if (typeof value === 'string') upstream = value
    }
    const costs: unknown = JSON.parse(props.channel?.upstream_costs || '{}')
    if (costs && typeof costs === 'object') {
      cost = (costs as Record<string, unknown>)[props.model]
    }
  } catch {
    cost = undefined
  }
  return (
    <div className='space-y-1 text-sm break-all'>
      <p className='font-medium'>
        {props.position}. {props.channel?.name || t('Unavailable channel')} (#
        {props.id})
      </p>
      <p>
        {t('Upstream model')}: {upstream}
      </p>
      <p>
        {t('Status')}:{' '}
        {props.channel?.status === 1 ? t('Enabled') : t('Unavailable channel')}
      </p>
      <p>
        {t('Configured model cost')}:{' '}
        {cost ? JSON.stringify(cost) : t('Unknown cost')}
      </p>
      {props.channel?.test_time ? (
        <p>
          {t('Last test')}:{' '}
          {new Date(props.channel.test_time * 1000).toLocaleString()} ·{' '}
          {props.channel.response_time} ms
        </p>
      ) : null}
    </div>
  )
}
