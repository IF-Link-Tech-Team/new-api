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
import { CircleAlert, QrCode } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { loadPaymentDisplayConfig } from '../lib/payment-display-config'

interface PaymentDisplayDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  productName: string
  amount: string
}

export function PaymentDisplayDialog(props: PaymentDisplayDialogProps) {
  const { t } = useTranslation()
  const [qrCodeUrl, setQrCodeUrl] = useState('')
  const [qrCodeFailed, setQrCodeFailed] = useState(false)

  useEffect(() => {
    if (!props.open) {
      return
    }

    let active = true
    setQrCodeFailed(false)
    void loadPaymentDisplayConfig().then((config) => {
      if (active) {
        setQrCodeUrl(config.alipayQrCodeUrl)
      }
    })

    return () => {
      active = false
    }
  }, [props.open])

  const showQrCode = Boolean(qrCodeUrl) && !qrCodeFailed

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <>
          <QrCode className='h-5 w-5' aria-hidden='true' />
          {t('Alipay collection')}
        </>
      }
      description={t('Review your payment details')}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'
      titleClassName='flex items-center gap-2'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <Button
          className='w-full sm:w-auto'
          onClick={() => props.onOpenChange(false)}
        >
          {t('Close')}
        </Button>
      }
    >
      <div className='bg-muted/50 space-y-3 rounded-lg border p-4'>
        <div className='flex items-start justify-between gap-4'>
          <span className='text-muted-foreground text-sm'>
            {t('Product Name')}
          </span>
          <span className='max-w-[240px] text-right text-sm font-medium break-words'>
            {props.productName}
          </span>
        </div>
        <div className='flex items-center justify-between gap-4 border-t pt-3'>
          <span className='text-sm font-medium'>{t('Amount Due')}</span>
          <span className='text-primary text-xl font-bold'>{props.amount}</span>
        </div>
      </div>

      <div
        className='bg-muted/30 mx-auto flex aspect-square w-full max-w-56 items-center justify-center overflow-hidden rounded-xl border border-dashed p-5'
        aria-label={t('Alipay QR code')}
      >
        {showQrCode ? (
          <img
            src={qrCodeUrl}
            alt={t('Alipay QR code')}
            className='h-full w-full object-contain'
            onError={() => setQrCodeFailed(true)}
          />
        ) : (
          <div
            className='text-muted-foreground flex flex-col items-center gap-3 text-center'
            data-payment-qr-placeholder='true'
          >
            <QrCode className='h-10 w-10' aria-hidden='true' />
            <span className='text-sm font-medium'>
              {t('Alipay QR code pending')}
            </span>
          </div>
        )}
      </div>

      <Alert>
        <CircleAlert aria-hidden='true' />
        <AlertDescription>
          {t(
            'Alipay payment integration is in progress. Online payment is currently unavailable.'
          )}
        </AlertDescription>
      </Alert>
    </Dialog>
  )
}
