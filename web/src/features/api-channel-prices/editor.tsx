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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { FieldGroup, FieldSet, FieldLegend } from '@/components/ui/field'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { PricingAmountInput } from '@/features/model-pricing/pricing-amount-input'
import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
} from '@/features/pricing/lib/billing-expr'
import { TieredPricingEditor } from '@/features/system-settings/models/tiered-pricing-editor'
import { handleServerError } from '@/lib/handle-server-error'

import { publishAPIChannelPrices, type ChannelPriceRow } from './api'
import {
  channelPricesSchema,
  priceFormDefaults,
  type ChannelPricesForm,
} from './lib'

export function ChannelPricesEditor(props: {
  rows: ChannelPriceRow[]
  revision: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [copyEpoch, setCopyEpoch] = useState<Record<number, number>>({})
  const form = useForm<ChannelPricesForm>({
    resolver: zodResolver(channelPricesSchema),
    defaultValues: priceFormDefaults(props.rows),
  })
  const draftRows = form.watch('rows')
  const save = useMutation({
    mutationFn: publishAPIChannelPrices,
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['api-channel-prices'] })
      props.onClose()
    },
    onError: (error) => handleServerError(error),
  })
  const submit = form.handleSubmit((values) =>
    save.mutate({
      expected_revision: props.revision,
      reason: values.reason,
      changes: values.rows.flatMap((value, index) => {
        if (!value.enabled && !props.rows[index].price) return []
        return [
          {
            model: props.rows[index].model,
            channel_id: props.rows[index].channel_id,
            upstream_model: props.rows[index].upstream_model,
            enabled: value.enabled,
            position: value.position === '' ? null : Number(value.position),
            selling_price: value.expression_mode
              ? ''
              : JSON.stringify({
                  input: Number(value.input),
                  cached_input: Number(value.cached_input),
                  cache_creation: Number(value.cache_creation),
                  output: Number(value.output),
                }),
            billing_expression: value.expression_mode
              ? value.billing_expression.trim()
              : '',
          },
        ]
      }),
    })
  )

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) props.onClose()
      }}
      title={t('Edit API channel prices')}
      description={props.rows[0]?.model}
      contentHeight='min(65dvh, 640px)'
      footer={
        <>
          <Button
            variant='outline'
            disabled={save.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='api-channel-price-form'
            disabled={save.isPending}
          >
            {t('Publish price revision')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='api-channel-price-form'
          onSubmit={(event) => {
            if (
              event.currentTarget.querySelector('[data-billing-invalid="true"]')
            ) {
              event.preventDefault()
              return
            }
            void submit(event)
          }}
        >
          <FieldGroup>
            <p className='text-muted-foreground text-sm'>
              {t(
                'USD per million tokens. Blank route positions inherit channel priority; otherwise number every enabled channel in order.'
              )}
            </p>
            {props.rows.map((row, index) => (
              <FieldSet
                key={row.channel_id}
                className='min-w-0 rounded-lg border p-4'
              >
                <FieldLegend className='max-w-full break-all'>
                  {row.channel_name} · #{row.channel_id}
                </FieldLegend>
                <p className='text-muted-foreground text-xs break-all'>
                  {row.upstream_model}
                </p>
                {!row.mapping_valid && (
                  <p role='alert' className='text-destructive text-sm'>
                    {t(
                      'Channel mapping is unavailable; this row can only be disabled.'
                    )}
                  </p>
                )}
                <FormField
                  control={form.control}
                  name={`rows.${index}.enabled`}
                  render={({ field }) => (
                    <FormItem className='flex items-center justify-between'>
                      <FormLabel>{t('Available for API sales')}</FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                          disabled={
                            (!row.mapping_valid && !field.value) ||
                            save.isPending
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name={`rows.${index}.expression_mode`}
                  render={({ field }) => (
                    <FormItem className='flex items-center justify-between gap-3'>
                      <FormLabel>
                        {t('Time-based or advanced pricing')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          disabled={save.isPending}
                          onCheckedChange={(enabled) => {
                            if (
                              enabled &&
                              !form.getValues(
                                `rows.${index}.billing_expression`
                              )
                            ) {
                              const value = form.getValues(`rows.${index}`)
                              if (
                                [
                                  value.input,
                                  value.cached_input,
                                  value.cache_creation,
                                  value.output,
                                ].every(
                                  (amount) =>
                                    amount.trim() !== '' &&
                                    Number.isFinite(Number(amount)) &&
                                    Number(amount) >= 0
                                )
                              ) {
                                form.setValue(
                                  `rows.${index}.billing_expression`,
                                  `tier("base", p * ${Number(value.input)} + cr * ${Number(value.cached_input)} + cc * ${Number(value.cache_creation)} + cc1h * ${Number(value.cache_creation)} + c * ${Number(value.output)})`,
                                  { shouldDirty: true }
                                )
                              }
                            }
                            field.onChange(enabled)
                          }}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                {!draftRows[index]?.expression_mode && (
                  <FieldGroup className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    {(
                      [
                        ['input', 'Input'],
                        ['cached_input', 'Cached Input'],
                        ['cache_creation', 'Cache Creation'],
                        ['output', 'Output'],
                      ] as const
                    ).map(([key, label]) => (
                      <FormField
                        key={key}
                        control={form.control}
                        name={`rows.${index}.${key}`}
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t(label)}</FormLabel>
                            <FormControl>
                              <PricingAmountInput
                                {...field}
                                onChange={field.onChange}
                                disabled={save.isPending}
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    ))}
                  </FieldGroup>
                )}
                <FormField
                  control={form.control}
                  name={`rows.${index}.position`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Model route position')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          inputMode='numeric'
                          placeholder={t('Inherit channel priority')}
                          disabled={save.isPending}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                {draftRows[index]?.expression_mode && (
                  <FormField
                    control={form.control}
                    name={`rows.${index}.billing_expression`}
                    render={({ field }) => {
                      const split = splitBillingExprAndRequestRules(field.value)
                      return (
                        <FormItem>
                          <FormLabel>
                            {t('Advanced billing expression')}
                          </FormLabel>
                          <fieldset
                            disabled={save.isPending}
                            className='min-w-0'
                          >
                            <TieredPricingEditor
                              key={`${row.channel_id}:${copyEpoch[index] ?? 0}`}
                              modelName={`${row.model}:${row.channel_id}`}
                              billingExpr={split.billingExpr}
                              requestRuleExpr={split.requestRuleExpr}
                              onBillingExprChange={(next) =>
                                field.onChange(
                                  combineBillingExpr(
                                    next,
                                    splitBillingExprAndRequestRules(
                                      form.getValues(
                                        `rows.${index}.billing_expression`
                                      )
                                    ).requestRuleExpr
                                  )
                                )
                              }
                              onRequestRuleExprChange={(next) =>
                                field.onChange(
                                  combineBillingExpr(
                                    splitBillingExprAndRequestRules(
                                      form.getValues(
                                        `rows.${index}.billing_expression`
                                      )
                                    ).billingExpr,
                                    next
                                  )
                                )
                              }
                            />
                          </fieldset>
                          <FormMessage />
                        </FormItem>
                      )
                    }}
                  />
                )}
                {index > 0 && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={save.isPending}
                    onClick={() => {
                      const source = form.getValues('rows.0')
                      for (const key of [
                        'input',
                        'cached_input',
                        'cache_creation',
                        'output',
                        'expression_mode',
                        'billing_expression',
                      ] as const) {
                        form.setValue(`rows.${index}.${key}`, source[key], {
                          shouldDirty: true,
                        })
                      }
                      setCopyEpoch((previous) => ({
                        ...previous,
                        [index]: (previous[index] ?? 0) + 1,
                      }))
                    }}
                  >
                    {t('Copy prices from first channel')}
                  </Button>
                )}
              </FieldSet>
            ))}
            <FormField
              control={form.control}
              name='reason'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Reason for change')}</FormLabel>
                  <FormControl>
                    <Input {...field} disabled={save.isPending} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </FieldGroup>
        </form>
      </Form>
    </Dialog>
  )
}
