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
import {
  CalendarClock,
  CreditCard,
  Package,
  RefreshCw,
  Settings2,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm, type Resolver } from 'react-hook-form'
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
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
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
import { getEnabledModels } from '@/features/channels/api'
import { getPaymentErrorTranslation } from '@/features/wallet/lib/payment'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createPlan,
  updatePlan,
  patchPlanFlags,
  getGroups,
  getAdminProductTemplates,
  createWaffoPancakeSubscriptionProduct,
  listWaffoPancakeSubscriptionProductOptions,
} from '../api'
import { getDurationUnitOptions, getResetPeriodOptions } from '../constants'
import {
  getPlanFormSchema,
  PLAN_FORM_DEFAULTS,
  planToFormValues,
  formValuesToPlanPayload,
  findTemplateForProductType,
  filterModelScopeOptions,
  formatTemplatePriceRules,
  parseTemplateModelScope,
  type PlanFormValues,
} from '../lib'
import type { PlanRecord } from '../types'
import { ProductPriceBookEditor } from './product-price-book-editor'
import { useSubscriptions } from './subscriptions-provider'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: PlanRecord
}

export function SubscriptionsMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: Props) {
  const { t } = useTranslation()
  const isEdit = !!currentRow?.plan?.id
  const productOwned = !isEdit || !!currentRow?.plan.price_book
  const [revision, setRevision] = useState(currentRow?.plan.config_revision)
  const { triggerRefresh } = useSubscriptions()
  const { meta: currencyMeta } = getCurrencyDisplay()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const currencyLabel = getCurrencyLabel()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [groupOptions, setGroupOptions] = useState<string[]>([])
  const [creatingPancakeProduct, setCreatingPancakeProduct] = useState(false)
  const [pancakeProducts, setPancakeProducts] = useState<
    { id: string; name: string; status: string }[]
  >([])

  const schema = getPlanFormSchema(t)
  const form = useForm<PlanFormValues>({
    resolver: zodResolver(schema) as unknown as Resolver<PlanFormValues>,
    defaultValues: PLAN_FORM_DEFAULTS,
  })

  useEffect(() => {
    if (open) {
      setRevision(currentRow?.plan.config_revision)
      if (currentRow?.plan) {
        form.reset(planToFormValues(currentRow.plan))
      } else {
        form.reset(PLAN_FORM_DEFAULTS)
      }
      getGroups()
        .then((res) => {
          if (res.success) {
            setGroupOptions(res.data || [])
          } else {
            handleServerError(res)
          }
        })
        .catch(() => {})
      // Best-effort — empty list still lets the operator use "+ Create".
      listWaffoPancakeSubscriptionProductOptions()
        .then((res) => {
          if (
            res.message === 'success' &&
            typeof res.data === 'object' &&
            res.data &&
            Array.isArray((res.data as { products?: unknown }).products)
          ) {
            setPancakeProducts(
              (res.data as { products: typeof pancakeProducts }).products
            )
          } else {
            setPancakeProducts([])
          }
        })
        .catch(() => setPancakeProducts([]))
    }
  }, [open, currentRow, form])

  const durationUnit = form.watch('duration_unit')
  const resetPeriod = form.watch('quota_reset_period')
  // Gate "+ Create on Pancake" on the same checks the mint handler runs.
  const watchedTitle = form.watch('title')
  const watchedPrice = form.watch('price_amount')
  const pancakeCreateReady =
    typeof watchedTitle === 'string' &&
    watchedTitle.trim().length > 0 &&
    Number(watchedPrice ?? 0) > 0

  // Only legacy plans retain template constraints during the migration.
  // Generic plans preserve selected models even when a channel is disabled.
  const { data: templatesData } = useQuery({
    queryKey: ['admin-product-templates'],
    queryFn: getAdminProductTemplates,
    enabled: open && !productOwned,
  })
  const templates = useMemo(() => templatesData?.data || [], [templatesData])
  const { data: enabledModelsData } = useQuery({
    queryKey: ['channel_models_enabled'],
    queryFn: getEnabledModels,
    enabled: open,
  })
  const enabledModels = useMemo(
    () => enabledModelsData?.data || [],
    [enabledModelsData]
  )

  const watchedProductType = form.watch('product_type')
  const selectedModels = form.watch('model_scope')
  const selectedTemplate = findTemplateForProductType(
    templates,
    watchedProductType || ''
  )
  const modelScopeOptions = useMemo(
    () =>
      (productOwned
        ? [...new Set([...enabledModels, ...selectedModels])]
        : filterModelScopeOptions(enabledModels, selectedTemplate)
      ).map((modelName) => ({ value: modelName, label: modelName })),
    [enabledModels, selectedTemplate, productOwned, selectedModels]
  )
  const templatePriceSummary = formatTemplatePriceRules(selectedTemplate)
  const templateModelScope = parseTemplateModelScope(selectedTemplate)

  const onSubmit = async (values: PlanFormValues) => {
    setIsSubmitting(true)
    try {
      const payload = formValuesToPlanPayload(
        values,
        currentRow?.plan,
        revision
      )
      if (isEdit && currentRow?.plan?.id) {
        const res = await updatePlan(currentRow.plan.id, payload)
        if (res.success) {
          // Legacy PUT preserves redemption_enabled. Generic PUT includes it
          // in the same revision-checked transaction as the other fields.
          if (
            !productOwned &&
            values.redemption_enabled !==
              (currentRow.plan.redemption_enabled !== false)
          ) {
            await patchPlanFlags(currentRow.plan.id, {
              redemption_enabled: values.redemption_enabled,
            })
          }
          toast.success(t('Update succeeded'))
          onOpenChange(false)
          triggerRefresh()
        } else {
          handleServerError(res)
        }
      } else {
        const res = await createPlan(payload)
        if (res.success) {
          toast.success(t('Create succeeded'))
          onOpenChange(false)
          triggerRefresh()
        } else {
          handleServerError(res)
        }
      }
    } catch (error) {
      handleServerError(error, t('Request failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  // Mints a Pancake OnetimeProduct (not SubscriptionProduct — see
  // controller) using persisted creds + the form's title/price, then
  // pins the returned PROD_ ID into the form field.
  const handleCreatePancakeProduct = async () => {
    const title = form.getValues('title').trim()
    const priceAmount = Number(form.getValues('price_amount') || 0)
    if (!title) {
      toast.error(t('Plan title is required'))
      return
    }
    if (priceAmount <= 0) {
      toast.error(t('Plan price must be greater than zero'))
      return
    }
    setCreatingPancakeProduct(true)
    try {
      const res = await createWaffoPancakeSubscriptionProduct({
        name: title,
        amount: priceAmount.toFixed(2),
      })
      if (
        res.message === 'success' &&
        typeof res.data === 'object' &&
        res.data
      ) {
        const created = res.data as { product_id: string; product_name: string }
        form.setValue('waffo_pancake_product_id', created.product_id, {
          shouldDirty: true,
        })
        // Refetch from GraphQL so the dropdown reflects authoritative state.
        try {
          const refresh = await listWaffoPancakeSubscriptionProductOptions()
          if (
            refresh.message === 'success' &&
            typeof refresh.data === 'object' &&
            refresh.data &&
            Array.isArray((refresh.data as { products?: unknown }).products)
          ) {
            setPancakeProducts(
              (refresh.data as { products: typeof pancakeProducts }).products
            )
          }
        } catch {
          // Best-effort — form value already points at the new product;
          // raw-ID fallback covers the missing label.
        }
        toast.success(
          `${t('Waffo Pancake product created')}: ${created.product_id}`
        )
      } else {
        const reason =
          getPaymentErrorTranslation(res) ??
          (typeof res.data === 'string' ? res.data : undefined)
        handleServerError(res.data, undefined, {
          title: reason
            ? `${t('Waffo Pancake product creation failed')}: ${reason}`
            : t('Waffo Pancake product creation failed'),
        })
      }
    } catch (err) {
      handleServerError(
        err,
        `${t('Waffo Pancake product creation failed')}: ${err instanceof Error ? err.message : String(err)}`
      )
    } finally {
      setCreatingPancakeProduct(false)
    }
  }

  const durationUnitOpts = getDurationUnitOptions(t)
  const resetPeriodOpts = getResetPeriodOptions(t)

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
          <SheetTitle>
            {isEdit ? t('Update plan info') : t('Create new subscription plan')}
          </SheetTitle>
          <SheetDescription>
            {isEdit
              ? t('Modify existing subscription plan configuration')
              : t(
                  'Fill in the following info to create a new subscription plan'
                )}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='subscription-form'
            onSubmit={form.handleSubmit(onSubmit)}
            className={sideDrawerFormClassName()}
          >
            {/* Basic Info */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='info' size='xs'>
                  <Settings2 />
                </IconBadge>
                {t('Basic Info')}
              </h3>

              <FormField
                control={form.control}
                name='title'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Title')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('e.g. Basic Plan')} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='subtitle'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Subtitle')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('e.g. Suitable for light usage')}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='price_amount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Plan Price')}</FormLabel>
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
                      <FormDescription>
                        {productOwned
                          ? t(
                              'Cash price in the selected currency; quota is configured separately.'
                            )
                          : t(
                              'Amount the user pays to purchase this plan; the actual currency depends on the payment gateway.'
                            )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='total_amount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Quota ({{currency}})', { currency: currencyLabel })}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          step={tokensOnly ? 1 : 0.01}
                          placeholder={
                            tokensOnly
                              ? t('Enter quota in tokens')
                              : t('Enter quota in {{currency}}', {
                                  currency: currencyLabel,
                                })
                          }
                          onChange={(e) =>
                            field.onChange(
                              Number.parseFloat(e.target.value) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Total quota included in the plan, usable per billing period. 0 means unlimited.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              {productOwned && (
                <FormField
                  control={form.control}
                  name='currency'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Currency')}</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={field.onChange}
                        items={[
                          { value: 'CNY', label: 'CNY' },
                          { value: 'USD', label: 'USD' },
                        ]}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='CNY'>CNY</SelectItem>
                            <SelectItem value='USD'>USD</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='upgrade_group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Upgrade Group')}</FormLabel>
                      <FormControl>
                        <Combobox
                          options={[
                            { value: '__none__', label: t('No Upgrade') },
                            ...groupOptions.map((g) => ({
                              value: g,
                              label: g,
                            })),
                          ]}
                          onValueChange={(v) =>
                            field.onChange(v === '__none__' ? '' : v)
                          }
                          value={field.value || ''}
                          className='w-full'
                          placeholder={t('No Upgrade')}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='downgrade_group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Downgrade Group')}</FormLabel>
                      <FormControl>
                        <Combobox
                          options={[
                            {
                              value: '__none__',
                              label: t('Downgrade to pre-purchase group'),
                            },
                            ...groupOptions.map((g) => ({
                              value: g,
                              label: g,
                            })),
                          ]}
                          onValueChange={(v) =>
                            field.onChange(v === '__none__' ? '' : v)
                          }
                          value={field.value || ''}
                          className='w-full'
                          placeholder={t('Downgrade to pre-purchase group')}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Downgrade to this group after the subscription expires'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='category'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Category')}</FormLabel>
                      <Select
                        items={[
                          {
                            value: 'package',
                            label: t('Traffic Pack'),
                          },
                          {
                            value: 'subscription',
                            label: t('Subscription'),
                          },
                        ]}
                        onValueChange={(v) => field.onChange(v)}
                        value={field.value || 'package'}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder={t('Traffic Pack')} />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='package'>
                              {t('Traffic Pack')}
                            </SelectItem>
                            <SelectItem value='subscription'>
                              {t('Subscription')}
                            </SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormDescription>
                        {t(
                          'Category controls where this product appears; quota and reset rules are configured independently.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='max_purchase_per_user'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Purchase Limit')}</FormLabel>
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
                      <FormDescription>
                        {t('0 means unlimited')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              {productOwned && (
                <FormField
                  control={form.control}
                  name='reservation_ttl_seconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Payment reservation lifetime (seconds)')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={1}
                          step={1}
                          onChange={(e) =>
                            field.onChange(Number(e.target.value))
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Unpaid orders reserve a purchase slot until this deadline. Payments received after release require manual compensation; quota is not issued automatically.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name='sort_order'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Sort Order')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
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

              <div className='flex flex-col gap-3'>
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
                          disabled={!isEdit}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='allow_balance_pay'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Allow balance redemption')}
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
                  name='allow_wallet_overflow'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Allow wallet balance after quota used up')}
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

            {/* Product Contract */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='chart-3' size='xs'>
                  <Package />
                </IconBadge>
                {productOwned
                  ? t('Product models and pricing')
                  : t('Product Contract')}
              </h3>

              {!productOwned && (
                <>
                  <FormField
                    control={form.control}
                    name='product_type'
                    render={({ field }) => {
                      const items = templates.map((template) => ({
                        value: template.product_type,
                        label: template.display_name
                          ? `${template.display_name} (${template.product_type})`
                          : template.product_type,
                      }))
                      if (
                        field.value &&
                        !templates.some(
                          (template) => template.product_type === field.value
                        )
                      ) {
                        items.push({ value: field.value, label: field.value })
                      }
                      return (
                        <FormItem>
                          <FormLabel>{t('Product Type')}</FormLabel>
                          {isEdit ? (
                            <FormControl>
                              <Input
                                value={field.value || t('None (legacy plan)')}
                                disabled
                              />
                            </FormControl>
                          ) : (
                            <Select
                              items={[
                                {
                                  value: '__none__',
                                  label: t('None (legacy plan)'),
                                },
                                ...items,
                              ]}
                              onValueChange={(v) =>
                                field.onChange(v === '__none__' ? '' : v)
                              }
                              value={field.value || '__none__'}
                            >
                              <FormControl>
                                <SelectTrigger>
                                  <SelectValue
                                    placeholder={t('None (legacy plan)')}
                                  />
                                </SelectTrigger>
                              </FormControl>
                              <SelectContent alignItemWithTrigger={false}>
                                <SelectGroup>
                                  <SelectItem value='__none__'>
                                    {t('None (legacy plan)')}
                                  </SelectItem>
                                  {items.map((item) => (
                                    <SelectItem
                                      key={item.value}
                                      value={item.value}
                                    >
                                      {item.label}
                                    </SelectItem>
                                  ))}
                                </SelectGroup>
                              </SelectContent>
                            </Select>
                          )}
                          <FormDescription>
                            {t(
                              'Product type binds the plan to a product template contract. It can only be chosen at creation time.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )
                    }}
                  />

                  {selectedTemplate && (
                    <div className='text-muted-foreground bg-muted/50 flex flex-col gap-1 rounded-md px-3 py-2 text-xs'>
                      <span className='text-foreground font-medium'>
                        {t('Template constraints')}
                        {selectedTemplate.locked
                          ? ` · ${t('Locked template')}`
                          : ''}
                      </span>
                      {templatePriceSummary && (
                        <span>
                          {t('Allowed prices for this type: {{prices}}', {
                            prices: templatePriceSummary,
                          })}
                        </span>
                      )}
                      {selectedTemplate.duration_days > 0 && (
                        <span>
                          {t('Fixed duration: {{days}} days', {
                            days: selectedTemplate.duration_days,
                          })}
                        </span>
                      )}
                      {selectedTemplate.billing_multiplier > 0 && (
                        <span>
                          {t('Billing multiplier: {{value}}', {
                            value: selectedTemplate.billing_multiplier,
                          })}
                        </span>
                      )}
                      {templateModelScope.length > 0 && (
                        <span>
                          {t('Allowed models: {{models}}', {
                            models: templateModelScope.join(', '),
                          })}
                        </span>
                      )}
                      {selectedTemplate.max_purchase_per_user > 0 && (
                        <span>
                          {t('Max purchases per user: {{count}}', {
                            count: selectedTemplate.max_purchase_per_user,
                          })}
                        </span>
                      )}
                      {selectedTemplate.price_version && (
                        <span>
                          {t('Price version: {{version}}', {
                            version: selectedTemplate.price_version,
                          })}
                        </span>
                      )}
                    </div>
                  )}

                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name='product_code'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Product Code')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              disabled={isEdit}
                              placeholder={t(
                                'e.g. deepseek-bigfish-lite-20261017'
                              )}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Unique product code; cannot be changed after creation'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='product_tier'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Product Tier')}</FormLabel>
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
                          <FormDescription>
                            {t(
                              'Tier index used by tiered templates; 0 when unused'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='product_family'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Product Family')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              placeholder={t('e.g. deepseek_classroom')}
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
                          <FormDescription>
                            {t(
                              'Plan-level billing multiplier; 0 lets the server apply the template default.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </>
              )}

              {productOwned && (
                <>
                  {!isEdit && (
                    <FormDescription>
                      {t(
                        'Save a draft first, then add model price versions before enabling sales or redemption.'
                      )}
                    </FormDescription>
                  )}
                  <FormField
                    control={form.control}
                    name='price_version'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Price Version')}</FormLabel>
                        <FormControl>
                          <Input {...field} placeholder='v2026-10' />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'All selected models must have an enabled price with this version before new sales can start.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  {currentRow?.plan && (
                    <ProductPriceBookEditor
                      product={currentRow.plan}
                      revision={revision || 0}
                      disabled={isSubmitting}
                      onRevisionChange={(nextRevision) => {
                        setRevision(nextRevision)
                        triggerRefresh()
                      }}
                    />
                  )}
                </>
              )}

              <FormField
                control={form.control}
                name='model_scope'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Model Scope')}</FormLabel>
                    <FormControl>
                      <MultiSelect
                        options={modelScopeOptions}
                        selected={field.value}
                        onChange={field.onChange}
                        placeholder={t('Select models allowed by this plan')}
                        emptyText={t('No models available')}
                        maxVisibleChips={6}
                        allowCreate={productOwned}
                        copyChipOnClick
                      />
                    </FormControl>
                    <FormDescription>
                      {productOwned
                        ? t(
                            'Select the exact models this product can use. Each model has its own product price.'
                          )
                        : t(
                            'Restricts which models may consume this plan. Empty means every model. Options are filtered to the template scope when one is selected.'
                          )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='flex flex-col gap-3'>
                <FormField
                  control={form.control}
                  name='listed'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Listed in catalog')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                          disabled={!isEdit}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='redemption_enabled'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Allow redemption codes')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                          disabled={!isEdit}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                {!productOwned && (
                  <FormField
                    control={form.control}
                    name='course_eligible'
                    render={({ field }) => (
                      <FormItem className={sideDrawerSwitchItemClassName()}>
                        <FormLabel className='!mt-0'>
                          {t('Course eligible')}
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
                )}

                {!productOwned && (
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
                )}
              </div>
            </SideDrawerSection>

            {/* Duration Settings */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='chart-4' size='xs'>
                  <CalendarClock />
                </IconBadge>
                {t('Duration Settings')}
              </h3>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='duration_unit'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Duration Unit')}</FormLabel>
                      <Select
                        items={durationUnitOpts.map((o) => ({
                          value: o.value,
                          label: o.label,
                        }))}
                        onValueChange={field.onChange}
                        value={field.value}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {durationUnitOpts.map((o) => (
                              <SelectItem key={o.value} value={o.value}>
                                {o.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {durationUnit === 'custom' ? (
                  <FormField
                    control={form.control}
                    name='custom_seconds'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Custom Seconds')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
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
                ) : (
                  <FormField
                    control={form.control}
                    name='duration_value'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Duration Value')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
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
                )}
              </div>
            </SideDrawerSection>

            {/* Quota Reset */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='success' size='xs'>
                  <RefreshCw />
                </IconBadge>
                {t('Quota Reset')}
              </h3>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='quota_reset_period'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Reset Cycle')}</FormLabel>
                      <Select
                        items={resetPeriodOpts.map((o) => ({
                          value: o.value,
                          label: o.label,
                        }))}
                        onValueChange={field.onChange}
                        value={field.value}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {resetPeriodOpts.map((o) => (
                              <SelectItem key={o.value} value={o.value}>
                                {o.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='quota_reset_custom_seconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Custom Seconds')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          disabled={resetPeriod !== 'custom'}
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
              </div>
            </SideDrawerSection>

            {/* Payment Config */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='warning' size='xs'>
                  <CreditCard />
                </IconBadge>
                {t('Third-party Payment Config')}
              </h3>

              <FormField
                control={form.control}
                name='stripe_price_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Stripe Price ID</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='price_...' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='creem_product_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Creem Product ID</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='prod_...' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='waffo_pancake_product_id'
                render={({ field }) => {
                  // Raw-ID fallback for IDs not yet in the catalog.
                  const items = pancakeProducts.map((p) => ({
                    value: p.id,
                    label: `${p.name} (${p.id})`,
                  }))
                  if (
                    field.value &&
                    !pancakeProducts.some((p) => p.id === field.value)
                  ) {
                    items.push({ value: field.value, label: field.value })
                  }
                  return (
                    <FormItem>
                      <FormLabel>Waffo Pancake Product ID</FormLabel>
                      <div className='flex gap-2'>
                        <Combobox
                          options={items}
                          value={field.value || ''}
                          onValueChange={(v) => field.onChange(v)}
                          disabled={items.length === 0}
                          className='w-full flex-1'
                          placeholder={t('Select a product')}
                        />
                        <Button
                          type='button'
                          variant='outline'
                          onClick={handleCreatePancakeProduct}
                          disabled={
                            creatingPancakeProduct || !pancakeCreateReady
                          }
                          className='shrink-0'
                        >
                          {creatingPancakeProduct
                            ? t('Creating...')
                            : `+ ${t('Create')}`}
                        </Button>
                      </div>
                      <FormDescription>
                        {t(
                          'Creates a Pancake product in the saved store using this plan’s title and price. Requires Waffo Pancake to be fully configured in Payment settings first.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )
                }}
              />
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='subscription-form'
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
