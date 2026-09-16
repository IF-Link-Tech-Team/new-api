/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Gift, Loader2, Receipt, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { cn } from '@/lib/utils'

import type { PaymentMethod, PresetAmount, TopupInfo } from '../types'

interface RechargeFormCardProps {
  topupInfo: TopupInfo | null
  presetAmounts: PresetAmount[]
  selectedPreset: number | null
  onSelectPreset: (preset: PresetAmount) => void
  topupAmount: number
  onPaymentMethodSelect: (method: PaymentMethod) => void
  paymentLoading: string | null
  redemptionCode: string
  onRedemptionCodeChange: (code: string) => void
  onRedeem: () => void
  redeeming: boolean
  loading?: boolean
  onOpenBilling?: () => void
}

export function RechargeFormCard({
  topupInfo,
  presetAmounts,
  selectedPreset,
  onSelectPreset,
  topupAmount,
  onPaymentMethodSelect,
  paymentLoading,
  redemptionCode,
  onRedemptionCodeChange,
  onRedeem,
  redeeming,
  loading,
  onOpenBilling,
}: RechargeFormCardProps) {
  const { t } = useTranslation()
  const frozenPresets = presetAmounts.filter((item) =>
    [10, 30, 50].includes(item.value)
  )
  const alipay: PaymentMethod = { name: '支付宝', type: 'alipay' }
  const onlineAvailable = topupInfo?.enable_alipay_topup === true
  const redemptionEnabled = topupInfo?.enable_redemption !== false

  if (loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-4 sm:p-5'>
          <Skeleton className='h-6 w-32' />
        </CardHeader>
        <CardContent className='space-y-4 p-4 sm:p-5'>
          <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
            {['10', '30', '50'].map((value) => (
              <Skeleton key={value} className='h-24 rounded-xl' />
            ))}
          </div>
        </CardContent>
      </Card>
    )
  }

  return (
    <TitledCard
      title='充值钱包余额'
      description='固定 ¥10、¥30、¥50 三档，1:1 到账，余额永久有效'
      icon={<WalletCards className='h-4 w-4' />}
      iconTone='success'
      disableHoverEffect
      action={
        onOpenBilling ? (
          <Button
            variant='outline'
            size='sm'
            onClick={onOpenBilling}
            className='w-full gap-2 whitespace-nowrap sm:w-auto'
          >
            <Receipt className='h-4 w-4' />
            {t('Order History')}
          </Button>
        ) : null
      }
      contentClassName='space-y-5'
    >
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
        {frozenPresets.map((preset) => (
          <Button
            key={preset.value}
            variant='outline'
            className={cn(
              'h-auto min-h-24 flex-col items-start rounded-xl px-4 py-3 text-left whitespace-normal',
              selectedPreset === preset.value
                ? 'border-primary bg-primary/5'
                : 'border-muted'
            )}
            onClick={() => onSelectPreset(preset)}
          >
            <span className='text-xl font-semibold'>¥{preset.value}</span>
            <span className='text-muted-foreground mt-1 text-xs font-normal'>
              钱包额度 ¥{preset.value}
            </span>
            <span className='text-muted-foreground text-xs font-normal'>
              官网价 ×1.0 · 永久有效
            </span>
          </Button>
        ))}
      </div>

      <Alert>
        <AlertDescription>
          余额 Key 只扣钱包余额，不会自动使用月卡或专项流量包。
        </AlertDescription>
      </Alert>

      {onlineAvailable ? (
        <Button
          className='w-full whitespace-nowrap'
          disabled={
            !frozenPresets.some((item) => item.value === topupAmount) ||
            !!paymentLoading
          }
          onClick={() => onPaymentMethodSelect(alipay)}
        >
          {paymentLoading === 'alipay' && (
            <Loader2 className='mr-2 h-4 w-4 animate-spin' />
          )}
          支付宝支付 ¥{topupAmount}
        </Button>
      ) : (
        <Alert>
          <AlertDescription>
            在线支付暂不可用。正式商户配置完成后，本页将开放支付宝入口。
          </AlertDescription>
        </Alert>
      )}

      {redemptionEnabled && (
        <div className='space-y-3 border-t pt-5'>
          <div className='flex items-center gap-2'>
            <Gift className='h-4 w-4 text-amber-500' />
            <Label htmlFor='redemption-code'>{t('Have a Code?')}</Label>
          </div>
          <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
            <Input
              id='redemption-code'
              value={redemptionCode}
              onChange={(event) => onRedemptionCodeChange(event.target.value)}
              placeholder={t('Enter redemption code')}
            />
            <Button
              variant='outline'
              onClick={onRedeem}
              disabled={!redemptionCode.trim() || redeeming}
              className='whitespace-nowrap'
            >
              {redeeming && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
              {t('Redeem')}
            </Button>
          </div>
        </div>
      )}
    </TitledCard>
  )
}
