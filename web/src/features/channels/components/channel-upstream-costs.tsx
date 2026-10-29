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
import { useState } from 'react'
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { FormField, FormItem, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'

import type { ChannelFormValues } from '../lib/channel-form'
import { COST_FIELDS, parseUpstreamCosts } from '../lib/upstream-costs'

export function ChannelUpstreamCosts(props: {
  disabled?: boolean
  channelId?: number
}) {
  const { t, i18n } = useTranslation()
  const [showHistory, setShowHistory] = useState(false)
  const [before, setBefore] = useState(0)
  const history = useQuery({
    queryKey: ['channel-cost-history', props.channelId, before],
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        data: {
          items: {
            id: number
            effective_at: number
            actor_id: number
            costs: string
          }[]
          next_id: number
        }
      }>(`/api/channel/${props.channelId}/cost-history`, { params: { before } })
      if (!response.data.success) {
        throw new Error(t('Failed to load cost history'))
      }
      return response.data.data
    },
    enabled: showHistory && !!props.channelId,
  })
  const form = useFormContext<ChannelFormValues>()
  const [search, setSearch] = useState('')
  const selectedModels = (form.watch('models') || '')
    .split(',')
    .map((value) => value.trim())
    .filter(Boolean)
  const labels = {
    input: t('Input'),
    cached_input: t('Cache read'),
    cache_creation: t('Cache write'),
    output: t('Output'),
  }
  return (
    <section
      className='space-y-4 rounded-lg border p-4'
      aria-label={t('Channel procurement costs')}
    >
      <h3 className='font-semibold'>{t('Channel procurement costs')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Supplier charges per million tokens, in the same currency as your cost analysis. These are procurement costs, not user selling prices. Saved with this channel.'
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Saving creates a price history version effective now. Previous calls keep their original cost version. Earlier unknown costs are not backfilled. Peak rules use Asia/Shanghai; list public holidays explicitly as off-peak dates.'
        )}
      </p>
      <Input
        aria-label={t('Filter cost models')}
        placeholder={t('Filter cost models')}
        value={search}
        onChange={(event) => setSearch(event.target.value)}
      />
      <FormField
        control={form.control}
        name='upstream_costs'
        render={({ field, fieldState }) => {
          const costs = parseUpstreamCosts(field.value)
          if (costs === null) {
            return (
              <FormItem>
                <p className='text-destructive text-sm'>
                  {t(
                    'Existing cost data is invalid. Check it before editing; it will not be silently replaced.'
                  )}
                </p>
                <FormMessage />
              </FormItem>
            )
          }
          const models = [
            ...new Set([...selectedModels, ...Object.keys(costs)]),
          ].filter((model) =>
            model.toLowerCase().includes(search.toLowerCase())
          )
          return (
            <FormItem>
              <div className='max-h-[480px] space-y-4 overflow-y-auto'>
                {models.map((model) => {
                  const peak = costs[model]?.peak
                  return (
                    <fieldset
                      key={model}
                      disabled={props.disabled}
                      className='space-y-3 rounded-lg border p-3 disabled:opacity-60'
                    >
                      <legend className='max-w-full px-1 text-sm break-all'>
                        {model}
                      </legend>
                      {!costs[model] ? (
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() =>
                            field.onChange(
                              JSON.stringify({
                                ...costs,
                                [model]: {
                                  input: null,
                                  cached_input: null,
                                  cache_creation: null,
                                  output: null,
                                  currency: 'CNY',
                                },
                              })
                            )
                          }
                        >
                          {t('Add procurement costs')}
                        </Button>
                      ) : (
                        <>
                          <Label className='grid gap-2 text-xs'>
                            {t('Procurement currency')}
                            <select
                              className='bg-background h-9 rounded-md border px-2'
                              aria-label={`${model} ${t('Procurement currency')}`}
                              value={costs[model].currency || ''}
                              onChange={(event) =>
                                field.onChange(
                                  JSON.stringify({
                                    ...costs,
                                    [model]: {
                                      ...costs[model],
                                      currency: event.target.value,
                                    },
                                  })
                                )
                              }
                            >
                              <option value=''>{t('Unknown currency')}</option>
                              <option value='CNY'>CNY</option>
                              <option value='USD'>USD</option>
                            </select>
                          </Label>
                          <p className='text-muted-foreground text-xs'>
                            {costs[model].peak
                              ? t('Off-peak costs')
                              : t('Fixed costs')}
                          </p>
                          <div className='grid grid-cols-2 gap-3'>
                            {COST_FIELDS.map((key) => (
                              <Label key={key} className='grid gap-2 text-xs'>
                                {labels[key]}
                                <Input
                                  type='number'
                                  min='0'
                                  step='any'
                                  aria-label={`${model} ${labels[key]}`}
                                  aria-invalid={fieldState.invalid}
                                  value={costs[model][key] ?? ''}
                                  onBlur={field.onBlur}
                                  onChange={(event) =>
                                    field.onChange(
                                      JSON.stringify({
                                        ...costs,
                                        [model]: {
                                          ...costs[model],
                                          [key]:
                                            event.target.value === ''
                                              ? null
                                              : Number(event.target.value),
                                        },
                                      })
                                    )
                                  }
                                />
                              </Label>
                            ))}
                          </div>
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            onClick={() => {
                              const entry = { ...costs[model] }
                              if (entry.peak) delete entry.peak
                              else {
                                entry.peak = {
                                  prices: {
                                    input: null,
                                    cached_input: null,
                                    cache_creation: null,
                                    output: null,
                                  },
                                  weekdays: [1, 2, 3, 4, 5],
                                  windows: ['09:00-12:00', '14:00-18:00'],
                                  offpeak_dates: [],
                                }
                              }
                              field.onChange(
                                JSON.stringify({ ...costs, [model]: entry })
                              )
                            }}
                          >
                            {costs[model].peak
                              ? t('Use fixed costs')
                              : t('Configure DeepSeek peak/off-peak')}
                          </Button>
                          {peak && (
                            <div className='space-y-3 rounded-md border p-3'>
                              <p className='text-xs font-medium'>
                                {t('Peak costs')}
                              </p>
                              <div className='grid grid-cols-2 gap-3'>
                                {COST_FIELDS.map((key) => (
                                  <Label
                                    key={key}
                                    className='grid gap-2 text-xs'
                                  >
                                    {labels[key]}
                                    <Input
                                      type='number'
                                      min='0'
                                      step='any'
                                      aria-label={`${model} ${t('Peak costs')} ${labels[key]}`}
                                      value={peak.prices[key] ?? ''}
                                      onChange={(event) => {
                                        field.onChange(
                                          JSON.stringify({
                                            ...costs,
                                            [model]: {
                                              ...costs[model],
                                              peak: {
                                                ...peak,
                                                prices: {
                                                  ...peak.prices,
                                                  [key]:
                                                    event.target.value === ''
                                                      ? null
                                                      : Number(
                                                          event.target.value
                                                        ),
                                                },
                                              },
                                            },
                                          })
                                        )
                                      }}
                                    />
                                  </Label>
                                ))}
                              </div>
                              <Label className='grid gap-2 text-xs'>
                                {t('Peak weekdays (1=Monday, 7=Sunday)')}
                                <Input
                                  aria-label={`${model} ${t('Peak weekdays (1=Monday, 7=Sunday)')}`}
                                  value={peak.weekdays.join(',')}
                                  onChange={(event) =>
                                    field.onChange(
                                      JSON.stringify({
                                        ...costs,
                                        [model]: {
                                          ...costs[model],
                                          peak: {
                                            ...peak,
                                            weekdays: event.target.value
                                              .split(',')
                                              .map(Number),
                                          },
                                        },
                                      })
                                    )
                                  }
                                />
                              </Label>
                              <Label className='grid gap-2 text-xs'>
                                {t('Peak windows (Shanghai, comma separated)')}
                                <Input
                                  aria-label={`${model} ${t('Peak windows (Shanghai, comma separated)')}`}
                                  value={peak.windows.join(',')}
                                  onChange={(event) =>
                                    field.onChange(
                                      JSON.stringify({
                                        ...costs,
                                        [model]: {
                                          ...costs[model],
                                          peak: {
                                            ...peak,
                                            windows:
                                              event.target.value.split(','),
                                          },
                                        },
                                      })
                                    )
                                  }
                                />
                              </Label>
                              <Label className='grid gap-2 text-xs'>
                                {t(
                                  'Public holidays / off-peak dates (YYYY-MM-DD, comma separated)'
                                )}
                                <Textarea
                                  aria-label={`${model} ${t('Public holidays / off-peak dates (YYYY-MM-DD, comma separated)')}`}
                                  value={peak.offpeak_dates.join(',')}
                                  onChange={(event) =>
                                    field.onChange(
                                      JSON.stringify({
                                        ...costs,
                                        [model]: {
                                          ...costs[model],
                                          peak: {
                                            ...peak,
                                            offpeak_dates: event.target.value
                                              ? event.target.value
                                                  .split(',')
                                                  .map((v) => v.trim())
                                              : [],
                                          },
                                        },
                                      })
                                    )
                                  }
                                />
                              </Label>
                            </div>
                          )}
                          <Button
                            type='button'
                            variant='ghost'
                            size='sm'
                            onClick={() => {
                              const next = { ...costs }
                              delete next[model]
                              field.onChange(JSON.stringify(next))
                            }}
                          >
                            {t('Remove cost reference')}
                          </Button>
                        </>
                      )}
                    </fieldset>
                  )
                })}
                {!models.length && (
                  <p className='text-muted-foreground text-sm'>
                    {t('No matching channel models')}
                  </p>
                )}
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Blank means unknown; enter zero only when the supplier does not charge that item. Removing a reference does not remove the model or change its selling price.'
                )}
              </p>
              <FormMessage />
            </FormItem>
          )
        }}
      />
      {props.channelId && (
        <div className='space-y-3 border-t pt-3'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setShowHistory(!showHistory)}
          >
            {t('Price history')}
          </Button>
          {showHistory && (
            <>
              {history.isLoading && <p>{t('Loading...')}</p>}
              {history.isError && (
                <p role='alert' className='text-destructive'>
                  {t('Failed to load cost history')}
                </p>
              )}
              {history.data?.items.map((version) => (
                <details key={version.id} className='rounded-md border p-3'>
                  <summary className='cursor-pointer text-xs'>
                    #{version.id} ·{' '}
                    {new Date(version.effective_at * 1000).toLocaleString(
                      toIntlLocale(i18n.resolvedLanguage || i18n.language),
                      { timeZone: 'Asia/Shanghai', hour12: false }
                    )}{' '}
                    · {t('Operator')} #{version.actor_id}
                  </summary>
                  <div className='mt-3 max-h-80 space-y-3 overflow-auto'>
                    {Object.entries(
                      parseUpstreamCosts(version.costs) || {}
                    ).map(([model, entry]) => (
                      <div
                        key={model}
                        className='space-y-1 rounded border p-2 text-xs'
                      >
                        <strong className='break-all'>
                          {model} · {entry.currency || t('Unknown currency')}
                        </strong>
                        <p>
                          {entry.peak ? t('Off-peak costs') : t('Fixed costs')}{' '}
                          ·{' '}
                          {COST_FIELDS.map(
                            (key) => `${labels[key]} ${entry[key] ?? '—'}`
                          ).join(' / ')}
                        </p>
                        {entry.peak && (
                          <>
                            <p>
                              {t('Peak costs')} ·{' '}
                              {COST_FIELDS.map(
                                (key) =>
                                  `${labels[key]} ${entry.peak?.prices[key] ?? '—'}`
                              ).join(' / ')}
                            </p>
                            <p>
                              {t('Peak weekdays (1=Monday, 7=Sunday)')}:{' '}
                              {entry.peak.weekdays.join(',')} ·{' '}
                              {entry.peak.windows.join(', ')}
                            </p>
                            <p>
                              {t(
                                'Public holidays / off-peak dates (YYYY-MM-DD, comma separated)'
                              )}
                              : {entry.peak.offpeak_dates.join(', ') || '—'}
                            </p>
                          </>
                        )}
                      </div>
                    ))}
                  </div>
                </details>
              ))}
              {history.data?.items.length === 0 && (
                <p>{t('No cost history yet')}</p>
              )}
              {!!history.data?.next_id && (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => setBefore(history.data?.next_id || 0)}
                >
                  {t('Older versions')}
                </Button>
              )}
              {before > 0 && (
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  onClick={() => setBefore(0)}
                >
                  {t('Latest versions')}
                </Button>
              )}
            </>
          )}
        </div>
      )}
    </section>
  )
}
