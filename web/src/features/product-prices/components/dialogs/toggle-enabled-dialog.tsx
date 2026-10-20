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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'

import { setProductPriceEnabled } from '../../api'
import { useProductPrices } from '../product-prices-provider'

export function ToggleEnabledDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useProductPrices()
  const [loading, setLoading] = useState(false)

  if (open !== 'toggle-enabled' || !currentRow) return null

  const isEnabled = currentRow.enabled
  const title = isEnabled ? t('Confirm disable') : t('Confirm enable')
  const description = isEnabled
    ? t(
        'After disabling, this price version stops selling, but historical purchases keep their settled price. Continue?'
      )
    : t('After enabling, this price version becomes sellable. Continue?')

  const handleConfirm = async () => {
    setLoading(true)
    try {
      const res = await setProductPriceEnabled(currentRow.id, !isEnabled)
      if (res.success) {
        toast.success(
          isEnabled ? t('Has been disabled') : t('Has been enabled')
        )
        triggerRefresh()
        setOpen(null)
      }
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <ConfirmDialog
      open
      onOpenChange={(v) => !v && setOpen(null)}
      title={title}
      desc={description}
      handleConfirm={handleConfirm}
      isLoading={loading}
      confirmText={isEnabled ? t('Disable') : t('Enable')}
      destructive={isEnabled}
    />
  )
}
