/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { CheckCircle2, Coins, CreditCard, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  getSubscriptionProduct,
  paySubscriptionIFLinkPay,
  redeemProductCode,
} from '@/features/subscriptions/api'
import type { ProductRedemptionResult } from '@/features/subscriptions/types'
import { ModelIdList } from '@/features/traffic-packs/components/model-id-list'
import {
  getProductComputePointsLabel,
  getProductDisplayName,
  isIfLearnPlan,
  parseModelScope,
} from '@/features/traffic-packs/lib/product-display'
import { getTopupInfo } from '@/features/wallet/api'

interface CheckoutProps {
  productId?: string
}

export function Checkout({ productId }: CheckoutProps) {
  const { t } = useTranslation()
  const productQuery = useQuery({
    queryKey: ['subscription-product', productId],
    queryFn: async () => {
      const response = await getSubscriptionProduct(productId ?? '')
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Product unavailable'))
      }
      return response.data
    },
    enabled: Boolean(productId),
    retry: false,
  })
  const paymentConfigQuery = useQuery({
    queryKey: ['topup-info', 'checkout-payment-availability'],
    queryFn: getTopupInfo,
    retry: false,
  })
  const [code, setCode] = useState('')
  const [redeeming, setRedeeming] = useState(false)
  const [paying, setPaying] = useState(false)
  const [result, setResult] = useState<ProductRedemptionResult | null>(null)
  const [redemptionError, setRedemptionError] = useState('')

  const plan = productQuery.data?.plan
  const productName = plan ? getProductDisplayName(plan) : ''
  const computePoints = plan ? getProductComputePointsLabel(plan) : null
  const models = parseModelScope(plan?.model_scope)
  const billingMultipliers = productQuery.data?.billing_multipliers || {}
  const ifLinkPayEnabled =
    paymentConfigQuery.data?.success === true &&
    paymentConfigQuery.data.data?.enable_iflinkpay_topup === true

  const submitPayment = async () => {
    if (!plan || paying || !ifLinkPayEnabled) return
    setPaying(true)
    try {
      const response = await paySubscriptionIFLinkPay({ plan_id: plan.id })
      const payURL = response.data?.pay_url
      if (!response.success || !payURL) {
        throw new Error(response.message || t('Payment request failed'))
      }
      const target = new URL(payURL, window.location.origin)
      if (target.protocol !== 'https:' && target.protocol !== 'http:') {
        throw new Error(t('Payment request failed'))
      }
      toast.success(t('Redirecting to payment page...'))
      window.location.href = target.toString()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Payment request failed')
      )
    } finally {
      setPaying(false)
    }
  }

  const submitRedemption = async () => {
    if (!productId || !code.trim() || redeeming) return
    setRedeeming(true)
    setRedemptionError('')
    try {
      const response = await redeemProductCode({
        key: code.trim(),
        product_id: productId,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Redemption failed'))
      }
      setResult(response.data)
      setCode('')
      toast.success(
        response.data.idempotent
          ? t(
              'This redemption was already completed. Your entitlement is available.'
            )
          : t(
              'Redemption successful. Your entitlement and dedicated API key are ready.'
            )
      )
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : t('Redemption failed. Please try again.')
      setRedemptionError(message)
      toast.error(message)
    } finally {
      setRedeeming(false)
    }
  }

  let paymentAction = (
    <Alert>
      <AlertDescription>
        {t(
          'Online topup is not enabled. Please use redemption code or contact administrator.'
        )}
      </AlertDescription>
    </Alert>
  )
  if (paymentConfigQuery.isLoading) {
    paymentAction = <Skeleton className='h-11 w-full rounded-md' />
  } else if (ifLinkPayEnabled) {
    paymentAction = (
      <Button
        className='w-full'
        size='lg'
        disabled={paying}
        onClick={() => void submitPayment()}
      >
        {paying ? <Loader2 className='animate-spin' /> : <CreditCard />}
        {t('Pay with IFLinkPay')}
      </Button>
    )
  }

  return (
    <main className='bg-muted/20 min-h-[100dvh] px-4 py-8 sm:py-12'>
      <section
        className='bg-background mx-auto w-full max-w-lg rounded-xl border p-5 shadow-sm sm:p-7'
        aria-labelledby='checkout-title'
      >
        <header className='mb-5 space-y-1'>
          <h1 id='checkout-title' className='text-xl font-semibold'>
            {t('Product Checkout')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('Review the product, then use its matching redemption code.')}
          </p>
        </header>

        {productQuery.isLoading ? (
          <div className='space-y-4' aria-live='polite'>
            <span className='sr-only'>{t('Loading...')}</span>
            <Skeleton className='h-24 w-full rounded-lg' />
            <Skeleton className='mx-auto aspect-square w-full max-w-56 rounded-xl' />
          </div>
        ) : null}

        {(!productId || productQuery.isError) && !productQuery.isLoading ? (
          <div className='space-y-5' role='alert'>
            <div>
              <h2 className='font-medium'>{t('Product unavailable')}</h2>
              <p className='text-muted-foreground mt-2 text-sm'>
                {t(
                  'This product does not exist, conflicts with another configuration, or is currently unavailable.'
                )}
              </p>
            </div>
            <Button
              className='w-full'
              variant='outline'
              render={<Link to='/' />}
            >
              {t('Back to Home')}
            </Button>
          </div>
        ) : null}

        {plan ? (
          <div className='space-y-5'>
            <div className='bg-muted/50 space-y-3 rounded-lg border p-4'>
              <div className='flex items-start justify-between gap-4'>
                <span className='text-muted-foreground text-sm'>
                  {t('Product Name')}
                </span>
                <span className='max-w-[240px] text-right text-sm font-medium break-words'>
                  {productName}
                </span>
              </div>
              <div className='flex items-center justify-between gap-4 border-t pt-3'>
                <span className='text-sm font-medium'>
                  {t('Amount Due (RMB)')}
                </span>
                <span className='text-primary text-xl font-bold'>
                  ¥{Number(plan.price_amount).toFixed(2)}
                </span>
              </div>
            </div>

            {paymentAction}

            <div className='space-y-2 rounded-lg border p-4 text-sm'>
              {isIfLearnPlan(plan) ? (
                <div className='bg-muted/35 grid gap-3 rounded-md p-3 sm:grid-cols-2'>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {t('Classroom model shared credit')}
                    </span>
                    <p className='mt-1 text-lg font-semibold tabular-nums'>
                      ¥{Number(plan.price_amount).toFixed(2)}
                    </p>
                  </div>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {t('Compute points')}
                    </span>
                    <p className='mt-1 flex items-center gap-2 text-lg font-semibold tabular-nums'>
                      <Coins className='h-4 w-4' />
                      {computePoints}
                    </p>
                  </div>
                </div>
              ) : null}
              <div className='flex justify-between gap-4'>
                <span className='text-muted-foreground'>{t('Validity')}</span>
                <span>
                  {t('{{count}} days', { count: plan.duration_value })}
                </span>
              </div>
              {models.length > 0 ? (
                <div className='border-t pt-3'>
                  <ModelIdList
                    models={models}
                    billingMultipliers={billingMultipliers}
                    title={t('Supported Models')}
                  />
                </div>
              ) : null}
            </div>

            <div className='space-y-3 border-t pt-5'>
              <div>
                <h2 className='font-medium'>{t('Have a redemption code?')}</h2>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t(
                    'The code must match this product. A failed attempt will not consume it.'
                  )}
                </p>
                {isIfLearnPlan(plan) ? (
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t(
                      'A valid code grants the product directly. No payment is required before redemption.'
                    )}
                  </p>
                ) : null}
              </div>
              <div className='flex flex-col gap-2 sm:flex-row'>
                <Input
                  value={code}
                  onChange={(event) => {
                    setCode(event.target.value)
                    if (redemptionError) setRedemptionError('')
                  }}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') void submitRedemption()
                  }}
                  placeholder={t('Enter redemption code')}
                  autoComplete='off'
                  aria-label={t('Redemption code')}
                  disabled={redeeming || !plan.redemption_enabled}
                />
                <Button
                  className='shrink-0'
                  disabled={
                    redeeming || !code.trim() || !plan.redemption_enabled
                  }
                  onClick={() => void submitRedemption()}
                >
                  {redeeming ? <Loader2 className='animate-spin' /> : null}
                  {t('Redeem now')}
                </Button>
              </div>
              {!plan.redemption_enabled ? (
                <p className='text-destructive text-sm'>
                  {t('Redemption is currently paused for this product.')}
                </p>
              ) : null}
              {redemptionError ? (
                <Alert variant='destructive' role='alert'>
                  <AlertDescription>{redemptionError}</AlertDescription>
                </Alert>
              ) : null}
            </div>

            {result ? (
              <Alert>
                <CheckCircle2 className='h-4 w-4 text-emerald-600' />
                <AlertDescription className='space-y-3'>
                  <p>
                    {t(
                      'Redemption successful. Your entitlement and dedicated API key are ready.'
                    )}
                  </p>
                  <div className='flex flex-wrap gap-2'>
                    {plan.category === 'subscription' ? (
                      <Button size='sm' render={<Link to='/plans' />}>
                        {t('View entitlement and API key')}
                      </Button>
                    ) : (
                      <Button size='sm' render={<Link to='/traffic-packs' />}>
                        {t('View entitlement and API key')}
                      </Button>
                    )}
                    <Button
                      size='sm'
                      variant='outline'
                      render={<Link to='/dashboard' />}
                    >
                      {t('Back to Dashboard')}
                    </Button>
                  </div>
                </AlertDescription>
              </Alert>
            ) : null}
          </div>
        ) : null}
      </section>
    </main>
  )
}
