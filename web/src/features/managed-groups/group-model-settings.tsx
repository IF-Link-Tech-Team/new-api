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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import {
  groupChange,
  groupGet,
  groupPath,
  type GroupChannel,
  type ManagedGroup,
} from './api'

type Prices = {
  input: number
  cached_input: number
  cache_creation: number
  output: number
}
export type GroupModelSetting = {
  channel_id: number
  channel_name: string
  model: string
  revision: number
  prices: Prices | null
  inherited_prices: Prices | null
}
const priceFields = [
  ['input', 'Input'],
  ['cached_input', 'Cache read'],
  ['cache_creation', 'Cache creation'],
  ['output', 'Output'],
] as const
const rateSchema = z.object({
  input: z.number().finite().nonnegative(),
  cached_input: z.number().finite().nonnegative(),
  cache_creation: z.number().finite().nonnegative(),
  output: z.number().finite().nonnegative(),
})
const modelsSchema = z.object({ models: z.string().trim().min(1) })

export function GroupModelSettings(props: { group: ManagedGroup }) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<GroupModelSetting | null>(null)
  const [channel, setChannel] = useState<GroupChannel | null>(null)
  const query = useQuery({
    queryKey: ['managed-groups', props.group.name, 'model-settings'],
    queryFn: () =>
      groupGet<GroupModelSetting[]>(`${groupPath(props.group.name)}/models`),
  })
  const channels = useQuery({
    queryKey: ['managed-groups', props.group.name, 'channels'],
    queryFn: () =>
      groupGet<GroupChannel[]>(`${groupPath(props.group.name)}/channels`),
  })
  const canManage = props.group.permissions.includes('channels.manage')
  const canPrice =
    canManage &&
    props.group.mode === 'independent' &&
    props.group.permissions.includes('wallet.manage')
  if (query.isPending || channels.isPending) return <LoadingState />
  if (query.isError || channels.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
          void channels.refetch()
        }}
      />
    )
  }
  return (
    <section className='space-y-6' aria-label={t('Group settings')}>
      <div className='space-y-2'>
        <h3 className='text-lg font-semibold'>{t('Models and group rates')}</h3>
        {props.group.mode === 'independent' && (
          <p className='text-muted-foreground max-w-3xl text-sm'>
            {t(
              'Group rates deduct only this group’s allocated credits. Personal balances and prices in other groups stay unchanged.'
            )}
          </p>
        )}
        <p className='text-muted-foreground text-sm'>
          {t(
            'Rates are in USD per million tokens. Unconfigured models keep their existing channel price; a zero rate is free.'
          )}
        </p>
      </div>
      {channels.data
        .filter((item) => item.owned)
        .map((item) => (
          <div key={item.id} className='space-y-3 rounded-xl border p-4 sm:p-5'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <div>
                <h4 className='font-semibold'>{item.name}</h4>
                <p className='text-muted-foreground text-sm'>
                  {t('Dedicated channel')} · #{item.id}
                </p>
              </div>
              {canManage && (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => setChannel(item)}
                >
                  {t('Manage models')}
                </Button>
              )}
            </div>
            <ul className='divide-y'>
              {query.data
                .filter((row) => row.channel_id === item.id)
                .map((row) => (
                  <li
                    key={row.model}
                    className='flex flex-wrap items-center justify-between gap-3 py-4'
                  >
                    <div className='min-w-0 flex-1 space-y-2'>
                      <p className='font-mono text-sm break-all'>{row.model}</p>
                      <Badge variant={row.prices ? 'secondary' : 'outline'}>
                        {row.prices
                          ? t('Group rate configured')
                          : t('Using existing channel price')}
                      </Badge>
                      {(row.prices || row.inherited_prices) && (
                        <p className='text-muted-foreground text-xs leading-6'>
                          {priceFields
                            .map(
                              ([key, label]) =>
                                `${t(label)} $${(row.prices || row.inherited_prices)?.[key]}`
                            )
                            .join(' · ')}
                        </p>
                      )}
                    </div>
                    {canPrice && (
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => setEditing(row)}
                        aria-label={t('Set rate for {{model}}', {
                          model: row.model,
                        })}
                      >
                        {t('Set group rate')}
                      </Button>
                    )}
                  </li>
                ))}
            </ul>
          </div>
        ))}
      {!query.data.length && (
        <EmptyState
          title={t('No dedicated group models')}
          description={t(
            'Create a dedicated channel from Group channels first.'
          )}
        />
      )}
      {editing && (
        <GroupRateDialog
          key={`${editing.channel_id}-${editing.model}`}
          group={props.group.name}
          model={editing}
          onClose={() => setEditing(null)}
        />
      )}
      {channel && (
        <GroupModelsDialog
          key={channel.id}
          group={props.group.name}
          channel={channel}
          onClose={() => setChannel(null)}
        />
      )}
    </section>
  )
}

function GroupRateDialog(props: {
  group: string
  model: GroupModelSetting
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const prices = props.model.prices
  const form = useForm({
    resolver: zodResolver(rateSchema),
    defaultValues: prices ?? {
      input: Number.NaN,
      cached_input: Number.NaN,
      cache_creation: Number.NaN,
      output: Number.NaN,
    },
  })
  const mutation = useMutation({
    mutationFn: (values: Prices) =>
      groupChange({
        method: 'PUT',
        url: `${groupPath(props.group)}/channels/${props.model.channel_id}/prices`,
        data: {
          model: props.model.model,
          prices: values,
          expected_revision: props.model.revision,
        },
      }),
    onSuccess: () => {
      toast.success(t('Saved'))
      void client.invalidateQueries({
        queryKey: ['managed-groups', props.group],
      })
      props.onClose()
    },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Set group rate')}
      description={`${props.group} · ${props.model.model}`}
    >
      <Form {...form}>
        <form
          className='space-y-5'
          onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
        >
          <p className='text-muted-foreground text-sm'>
            {t(
              'USD per million tokens. Enter every rate explicitly; zero means free. Changes apply to new requests only.'
            )}
          </p>
          <div className='grid gap-4 sm:grid-cols-2'>
            {priceFields.map(([key, label]) => (
              <FormField
                key={key}
                control={form.control}
                name={key}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t(label)}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        step='any'
                        {...field}
                        value={Number.isNaN(field.value) ? '' : field.value}
                        onChange={(event) =>
                          field.onChange(
                            event.target.value === ''
                              ? Number.NaN
                              : Number(event.target.value)
                          )
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            ))}
          </div>
          <div className='flex justify-end gap-2'>
            <Button type='button' variant='outline' onClick={props.onClose}>
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={mutation.isPending}>
              {t('Save')}
            </Button>
          </div>
        </form>
      </Form>
    </Dialog>
  )
}

function GroupModelsDialog(props: {
  group: string
  channel: GroupChannel
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm({
    resolver: zodResolver(modelsSchema),
    defaultValues: { models: props.channel.models },
  })
  const mutation = useMutation({
    mutationFn: (values: z.infer<typeof modelsSchema>) =>
      groupChange({
        method: 'PUT',
        url: `${groupPath(props.group)}/channels/${props.channel.id}/models`,
        data: { ...values, expected_models: props.channel.models },
      }),
    onSuccess: () => {
      toast.success(t('Saved'))
      void client.invalidateQueries({
        queryKey: ['managed-groups', props.group],
      })
      props.onClose()
    },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Manage models')}
      description={props.channel.name}
    >
      <Form {...form}>
        <form
          className='space-y-4'
          onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
        >
          <p className='text-muted-foreground text-sm'>
            {t(
              'Changes affect only this dedicated channel. New models need a group rate or an existing channel price before use. Removing a model stops new calls to it.'
            )}
          </p>
          <FormField
            control={form.control}
            name='models'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Models (comma-separated)')}</FormLabel>
                <FormControl>
                  <Textarea {...field} rows={5} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <div className='flex justify-end gap-2'>
            <Button type='button' variant='outline' onClick={props.onClose}>
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={mutation.isPending}>
              {t('Save')}
            </Button>
          </div>
        </form>
      </Form>
    </Dialog>
  )
}
