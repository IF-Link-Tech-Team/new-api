/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'

interface ModelIdListProps {
  models: string[]
  billingMultipliers?: Record<string, number>
  title?: string
}

export function ModelIdList({
  models,
  billingMultipliers = {},
  title = '可用模型',
}: ModelIdListProps) {
  const { t } = useTranslation()
  if (models.length === 0) return null

  return (
    <div className='space-y-2'>
      <p className='text-muted-foreground text-xs font-medium'>{title}</p>
      <div className='grid gap-2'>
        {models.map((model) => {
          const multiplier = billingMultipliers[model]
          return (
            <div
              key={model}
              className='bg-muted/40 flex min-w-0 items-center gap-2 rounded-md border px-2.5 py-2'
            >
              <code className='min-w-0 flex-1 text-xs font-medium break-all sm:text-sm'>
                {model}
              </code>
              {typeof multiplier === 'number' && Number.isFinite(multiplier) ? (
                <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>
                  {t('Versioned base price')} ×{multiplier.toFixed(2)}
                </span>
              ) : null}
              <CopyButton
                value={model}
                className='h-10 w-10 sm:h-8 sm:w-8'
                iconClassName='h-4 w-4'
                tooltip='复制模型 ID'
                successTooltip='模型 ID 已复制'
                notify
                successMessage='模型 ID 已复制'
                errorMessage='复制模型 ID 失败'
                aria-label={`复制模型 ID ${model}`}
              />
            </div>
          )
        })}
      </div>
    </div>
  )
}
