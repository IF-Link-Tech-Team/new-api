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
import { useQuery } from '@tanstack/react-query'
import { DollarSign, Package } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm, type Control, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { getAdminProductTemplates } from '@/features/subscriptions/api'
import type { SubscriptionPlan } from '@/features/subscriptions/types'
import { handleServerError } from '@/lib/handle-server-error'

import { createProductPrice, createProductPriceBookVersion } from '../api'
import {
  getPriceFormSchema,
  PRICE_FORM_DEFAULTS,
  formValuesToPricePayload,
  type PriceFormValues,
} from '../lib'
import { ProductChannelOrder } from './product-channel-order'
import { useProductPrices } from './product-prices-provider'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type PriceComponentPrefix = 'official' | 'selling' | 'upstream' | 'cost'

// One row of all four price components for a
// price field; every price JSON string is edited through the same shape.
function PriceComponentInputs({
  control,
  prefix,
  label,
}: {
  control: Control<PriceFormValues>
  prefix: PriceComponentPrefix
  label: string
}) {
  const { t } = useTranslation()
  const fields = [
    { name: `${prefix}_input`, label: t('Input') },
    { name: `${prefix}_cached_input`, label: t('Cached Input') },
    { name: `${prefix}_cache_creation`, label: t('Cache Creation') },
    { name: `${prefix}_output`, label: t('Output') },
  ] as const
  return (
    <div className='flex flex-col gap-2'>
      <span className='text-sm font-medium'>{label}</span>
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        {fields.map((item) => (
          <FormField
            key={item.name}
            control={control}
            name={item.name}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{item.label}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='number'
                    step='0.0001'
                    min={0}
                    onChange={(e) =>
                      field.onChange(Number.parseFloat(e.target.value) || 0)
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}
      </div>
    </div>
  )
}

// Price rows are immutable; this drawer only ever creates a new version.
export function ProductPricesMutateDrawer(props: Props) {
  const { triggerRefresh } = useProductPrices()
  return <ProductPriceVersionDrawer {...props} onCreated={triggerRefresh} />
}

export function ProductPriceVersionDrawer(
  props: Props & { product?: SubscriptionPlan; onCreated: () => void }
) {
  const { open, onOpenChange } = props
  const { t } = useTranslation()
  const [isSubmitting, setIsSubmitting] = useState(false)

  const schema = getPriceFormSchema(t, !!props.product)
  const form = useForm<PriceFormValues>({
    resolver: zodResolver(schema) as unknown as Resolver<PriceFormValues>,
    defaultValues: PRICE_FORM_DEFAULTS,
  })

  const { data: templatesData } = useQuery({
    queryKey: ['admin-product-templates'],
    queryFn: getAdminProductTemplates,
    enabled: open && !props.product,
  })
  const templates = templatesData?.data || []

  useEffect(() => {
    if (open) {
      form.reset({
        ...PRICE_FORM_DEFAULTS,
        effective_at: Math.floor(Date.now() / 1000),
        price_version: props.product?.price_version || '',
      })
    }
  }, [open, form, props.product?.price_version])

  const onSubmit = async (values: PriceFormValues) => {
    const payload = formValuesToPricePayload(values)
    if (!payload) {
      form.setError('allowed_channels', {
        message: t(
          'Channel IDs must be a comma-separated list of unique positive integers'
        ),
      })
      return
    }
    setIsSubmitting(true)
    try {
      const res = props.product
        ? await createProductPriceBookVersion(props.product.id, {
            price: { ...payload.price, route_mode: values.route_mode },
          })
        : await createProductPrice(payload)
      if (res.success) {
        toast.success(t('Create succeeded'))
        onOpenChange(false)
        props.onCreated()
      }
    } catch (error) {
      handleServerError(error, t('Request failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  const watchedProductType = form.watch('product_type')
  const selectedTemplate = templates.find(
    (template) => template.product_type === watchedProductType
  )

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) {
          form.reset()
        }
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[600px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Add Price Version')}</SheetTitle>
          <SheetDescription>
            {t(
              'Price rows are immutable once created; repricing always means adding a new price version.'
            )}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='product-price-form'
            onSubmit={form.handleSubmit(onSubmit)}
            className={sideDrawerFormClassName()}
          >
            {/* Version Identity */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='chart-3' size='xs'>
                  <Package />
                </IconBadge>
                {t('Version Identity')}
              </h3>

              {!props.product && (
                <FormField
                  control={form.control}
                  name='product_type'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Product Type')}</FormLabel>
                      <Select
                        items={templates.map((template) => ({
                          value: template.product_type,
                          label: template.display_name
                            ? `${template.display_name} (${template.product_type})`
                            : template.product_type,
                        }))}
                        onValueChange={field.onChange}
                        value={field.value || ''}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue
                              placeholder={t('Select a product type')}
                            />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {templates.map((template) => (
                              <SelectItem
                                key={template.product_type}
                                value={template.product_type}
                              >
                                {template.display_name
                                  ? `${template.display_name} (${template.product_type})`
                                  : template.product_type}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              {props.product && (
                <FormDescription>
                  {props.product.title}
                  <br />
                  {t(
                    'Model rates and cost limits use USD per million tokens, independently of the product cash currency.'
                  )}
                </FormDescription>
              )}

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='model'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Model')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          placeholder={t(
                            'Exact model ID, e.g. deepseek-v4-flash'
                          )}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='price_version'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Price Version')}</FormLabel>
                      <FormControl>
                        <Input {...field} placeholder='v2026-10' />
                      </FormControl>
                      {selectedTemplate?.price_version && (
                        <FormDescription>
                          {t('Template price version: {{version}}', {
                            version: selectedTemplate.price_version,
                          })}
                        </FormDescription>
                      )}
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='effective_at'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Effective At (unix seconds)')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='billing_multiplier'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Billing Multiplier')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          step='0.01'
                          min={0}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseFloat(e.target.value) || 0
                            )
                          }
                        />
                      </FormControl>
                      {selectedTemplate && (
                        <FormDescription>
                          {t('Template billing multiplier: {{value}}', {
                            value: selectedTemplate.billing_multiplier,
                          })}
                        </FormDescription>
                      )}
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
                name='billing_expression'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Billing Expression')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder='tier("default", p * 1 + cr * 0.2 + c * 4)'
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {props.product && (
                <FormField
                  control={form.control}
                  name='route_mode'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Product route selection')}</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={field.onChange}
                      >
                        <FormControl>
                          <SelectTrigger className='w-full'>
                            <SelectValue>
                              {field.value === 'ordered'
                                ? t('Product channel order')
                                : t('Global channel priority and weight')}
                            </SelectValue>
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value='global'>
                              {t('Global channel priority and weight')}
                            </SelectItem>
                            <SelectItem value='ordered'>
                              {t('Product channel order')}
                            </SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormDescription>
                        {t(
                          'Global routing follows future channel priority changes. Product order stays with this price version; existing entitlements keep their version.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name='allowed_channels'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Allowed Channels')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='14, 15' />
                    </FormControl>
                    {props.product && (
                      <ProductChannelOrder
                        model={form.watch('model')}
                        value={field.value}
                        onChange={field.onChange}
                        disabled={isSubmitting}
                      />
                    )}
                    <FormDescription>
                      {props.product && form.watch('route_mode') === 'ordered'
                        ? t(
                            'Enter channel IDs in order: primary, first backup, second backup. Disable fallback to use only the first channel. All candidates must pass availability and cost checks.'
                          )
                        : t(
                            'Comma-separated channel IDs that may serve this price version'
                          )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='flex flex-col gap-3'>
                <FormField
                  control={form.control}
                  name='fallback_allowed'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Allow route fallback')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='enabled'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Enabled Status')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
              </div>
            </SideDrawerSection>

            {/* Price Components */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='warning' size='xs'>
                  <DollarSign />
                </IconBadge>
                {t('Price Components')}
              </h3>

              <PriceComponentInputs
                control={form.control}
                prefix='official'
                label={t('Official Reference Price')}
              />
              <PriceComponentInputs
                control={form.control}
                prefix='selling'
                label={t('User Selling Price')}
              />
              <PriceComponentInputs
                control={form.control}
                prefix='upstream'
                label={t('Upstream Settlement Price')}
              />
              <PriceComponentInputs
                control={form.control}
                prefix='cost'
                label={t('Max Upstream Cost')}
              />
              <FormDescription>
                {t(
                  'All components are per-million-token prices; the settlement price must not exceed the max upstream cost.'
                )}
              </FormDescription>
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='product-price-form'
            type='submit'
            disabled={isSubmitting}
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
