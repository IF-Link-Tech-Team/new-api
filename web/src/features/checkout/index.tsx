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
import { Link } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { PaymentDisplayPanel } from '@/features/payment-display/components/payment-display-panel'
import { loadPurchaseCatalogProduct } from '@/features/payment-display/lib/purchase-catalog'
import {
  getPublicPlans,
  paySubscriptionAlipay,
} from '@/features/subscriptions/api'
import type { PlanRecord } from '@/features/subscriptions/types'

import {
  findTrustedCheckoutPlan,
  formatCheckoutAmount,
} from './lib/checkout-product'

interface CheckoutProps {
  productId?: string
}

export function Checkout(props: CheckoutProps) {
  const { t } = useTranslation()
  const productQuery = useQuery({
    queryKey: ['purchase-catalog-product', props.productId],
    queryFn: () => loadPurchaseCatalogProduct(props.productId ?? ''),
    enabled: Boolean(props.productId),
    retry: false,
  })
  const plansQuery = useQuery<PlanRecord[]>({
    queryKey: ['subscription-plans', 'checkout'],
    queryFn: async () => {
      const response = await getPublicPlans()
      if (!response.success) {
        throw new Error(response.message || 'Unable to load subscription plans')
      }
      return response.data ?? []
    },
    enabled: Boolean(props.productId),
    retry: false,
  })
  const [paying, setPaying] = useState(false)

  const selectedPlan = useMemo(
    () => findTrustedCheckoutPlan(props.productId ?? '', plansQuery.data ?? []),
    [plansQuery.data, props.productId]
  )

  const isLoading = productQuery.isLoading || plansQuery.isLoading

  const productUnavailable =
    !props.productId ||
    productQuery.isError ||
    productQuery.data === null ||
    plansQuery.isError ||
    (!isLoading && selectedPlan === null)

  const handlePay = async () => {
    if (!selectedPlan || paying) return

    setPaying(true)
    try {
      const response = await paySubscriptionAlipay({
        plan_id: selectedPlan.plan.id,
      })
      const payUrl = response.data?.pay_url
      if (!response.success || !payUrl) {
        toast.error(response.message || t('Payment request failed'))
        return
      }

      if (response.data?.trade_no) {
        sessionStorage.setItem(
          'cilai_pending_subscription_order',
          response.data.trade_no
        )
      }
      toast.success(t('Payment page opened'))
      window.location.assign(payUrl)
    } catch {
      toast.error(t('Payment request failed'))
    } finally {
      setPaying(false)
    }
  }

  return (
    <main className='bg-muted/20 flex min-h-[100dvh] items-center px-4 py-8 sm:py-12'>
      <section
        className='bg-background mx-auto w-full max-w-lg rounded-xl border p-5 shadow-sm sm:p-7'
        aria-labelledby='checkout-title'
      >
        <header className='mb-5 space-y-1'>
          <h1 id='checkout-title' className='text-xl font-semibold'>
            {t('Payment')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('Review your payment details')}
          </p>
        </header>

        {isLoading ? (
          <div className='space-y-4' aria-live='polite'>
            <Skeleton className='h-24 w-full rounded-lg' />
            <Skeleton className='mx-auto aspect-square w-full max-w-56 rounded-xl' />
            <span className='sr-only'>{t('Loading...')}</span>
          </div>
        ) : null}

        {productUnavailable && !isLoading ? (
          <div className='space-y-5' role='alert'>
            <div className='space-y-2'>
              <h2 className='font-medium'>{t('Product unavailable')}</h2>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'The selected product could not be loaded. Please return to the product list and try again.'
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

        {productQuery.data && selectedPlan ? (
          <PaymentDisplayPanel
            productName={selectedPlan.plan.title || productQuery.data.name}
            amount={formatCheckoutAmount(selectedPlan)}
            displayOnly={false}
            onPay={handlePay}
            paying={paying}
          />
        ) : null}
      </section>
    </main>
  )
}
