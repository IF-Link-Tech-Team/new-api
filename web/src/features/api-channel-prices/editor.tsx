import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
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
  const form = useForm<ChannelPricesForm>({
    resolver: zodResolver(channelPricesSchema),
    defaultValues: priceFormDefaults(props.rows),
  })
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
            selling_price: JSON.stringify({
              input: Number(value.input),
              cached_input: Number(value.cached_input),
              cache_creation: Number(value.cache_creation),
              output: Number(value.output),
            }),
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
        <form id='api-channel-price-form' onSubmit={submit}>
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
                      ] as const) {
                        form.setValue(`rows.${index}.${key}`, source[key], {
                          shouldDirty: true,
                        })
                      }
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
