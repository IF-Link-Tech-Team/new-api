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
import { QrCode } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

import { loadPaymentDisplayConfig } from '../lib/payment-display-config'
import { PaymentDisplayPanel } from './payment-display-panel'

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
      footer={
        <Button
          className='w-full sm:w-auto'
          onClick={() => props.onOpenChange(false)}
        >
          {t('Close')}
        </Button>
      }
    >
      <PaymentDisplayPanel
        productName={props.productName}
        amount={props.amount}
        qrCodeUrl={showQrCode ? qrCodeUrl : undefined}
        onQrCodeError={() => setQrCodeFailed(true)}
      />
    </Dialog>
  )
}
