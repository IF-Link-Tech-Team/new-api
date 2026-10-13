/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { CircleAlert, QrCode } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'

interface PaymentDisplayPanelProps {
  productName: string
  amount: string
  qrCodeUrl?: string
  onQrCodeError?: () => void
}

export function PaymentDisplayPanel(props: PaymentDisplayPanelProps) {
  const { t } = useTranslation()
  return (
    <div className='space-y-4'>
      <div className='bg-muted/50 space-y-3 rounded-lg border p-4'>
        <div className='flex items-start justify-between gap-4'>
          <span className='text-muted-foreground text-sm'>
            {t('Product Name')}
          </span>
          <span className='max-w-[260px] text-right text-sm font-medium break-words'>
            {props.productName}
          </span>
        </div>
        <div className='flex items-center justify-between gap-4 border-t pt-3'>
          <span className='text-sm font-medium'>{t('List price')}</span>
          <span className='text-primary text-xl font-bold'>{props.amount}</span>
        </div>
      </div>

      <div
        className='bg-muted/30 mx-auto flex aspect-square w-full max-w-56 items-center justify-center overflow-hidden rounded-xl border border-dashed p-5'
        aria-label={t('Alipay QR code')}
      >
        {props.qrCodeUrl ? (
          <img
            src={props.qrCodeUrl}
            alt={t('Alipay QR code')}
            className='h-full w-full object-contain'
            onError={props.onQrCodeError}
          />
        ) : (
          <div className='text-muted-foreground flex flex-col items-center gap-3 text-center'>
            <QrCode className='h-10 w-10' aria-hidden='true' />
            <span className='text-sm font-medium'>
              {t('Payment QR code is not available yet')}
            </span>
          </div>
        )}
      </div>

      <Alert>
        <CircleAlert aria-hidden='true' />
        <AlertDescription>
          {t(
            'Online payment is not open yet. You can still redeem a code below.'
          )}
        </AlertDescription>
      </Alert>
    </div>
  )
}
