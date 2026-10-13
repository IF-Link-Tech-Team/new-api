/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { CheckCircle2, Coins, Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { PaymentDisplayPanel } from '@/features/payment-display/components/payment-display-panel'
import { loadPaymentDisplayConfig } from '@/features/payment-display/lib/payment-display-config'
import {
  getSubscriptionProduct,
  redeemProductCode,
} from '@/features/subscriptions/api'
import type { ProductRedemptionResult } from '@/features/subscriptions/types'
import {
  getProductComputePointsLabel,
  getProductDisplayName,
  isIfLearnPlan,
} from '@/features/traffic-packs/lib/product-display'

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
  const [qrCodeUrl, setQrCodeUrl] = useState('')
  const [code, setCode] = useState('')
  const [redeeming, setRedeeming] = useState(false)
  const [result, setResult] = useState<ProductRedemptionResult | null>(null)
  const [redemptionError, setRedemptionError] = useState('')

  useEffect(() => {
    let active = true
    void loadPaymentDisplayConfig().then((config) => {
      if (active) setQrCodeUrl(config.alipayQrCodeUrl)
    })
    return () => {
      active = false
    }
  }, [])

  const plan = productQuery.data?.plan
  const productName = plan ? getProductDisplayName(plan) : ''
  const computePoints = plan ? getProductComputePointsLabel(plan) : null
  const models = (plan?.model_scope || '')
    .split(',')
    .map((model) => model.trim())
    .filter(Boolean)

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

  return (
    <main className='bg-muted/20 min-h-[100dvh] px-4 py-8 sm:py-12'>
      <section className='bg-background mx-auto w-full max-w-lg rounded-xl border p-5 shadow-sm sm:p-7'>
        <header className='mb-5 space-y-1'>
          <h1 className='text-xl font-semibold'>{t('Product Checkout')}</h1>
          <p className='text-muted-foreground text-sm'>
            {t('Review the product, then use its matching redemption code.')}
          </p>
        </header>

        {productQuery.isLoading ? (
          <div className='space-y-4'>
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
            <PaymentDisplayPanel
              productName={productName}
              amount={`¥${Number(plan.price_amount).toFixed(2)}`}
              qrCodeUrl={qrCodeUrl || undefined}
              onQrCodeError={() => setQrCodeUrl('')}
            />

            <div className='space-y-2 rounded-lg border p-4 text-sm'>
              {isIfLearnPlan(plan) ? (
                <div className='bg-muted/35 grid gap-3 rounded-md p-3 sm:grid-cols-2'>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {t('DeepSeek call credit')}
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
                <div className='space-y-2 border-t pt-3'>
                  <span className='text-muted-foreground'>
                    {t('Supported Models')}
                  </span>
                  <div className='flex flex-wrap gap-1.5'>
                    {models.map((model) => (
                      <Badge key={model} variant='secondary'>
                        {model}
                      </Badge>
                    ))}
                  </div>
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
                      <Button size='sm' render={<Link to='/subscriptions' />}>
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
