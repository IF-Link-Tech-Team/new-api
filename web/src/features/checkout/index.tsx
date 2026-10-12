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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { PaymentDisplayPanel } from '@/features/payment-display/components/payment-display-panel'
import { loadPurchaseCatalogProduct } from '@/features/payment-display/lib/purchase-catalog'

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

  const productUnavailable =
    !props.productId || productQuery.isError || productQuery.data === null

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

        {productQuery.isLoading ? (
          <div className='space-y-4' aria-live='polite'>
            <Skeleton className='h-24 w-full rounded-lg' />
            <Skeleton className='mx-auto aspect-square w-full max-w-56 rounded-xl' />
            <span className='sr-only'>{t('Loading...')}</span>
          </div>
        ) : null}

        {productUnavailable && !productQuery.isLoading ? (
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

        {productQuery.data ? (
          <PaymentDisplayPanel
            productName={productQuery.data.name}
            amount={productQuery.data.amount}
          />
        ) : null}
      </section>
    </main>
  )
}
