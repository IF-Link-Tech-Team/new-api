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
import { Pencil } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { PrivateUserAliasDialog } from './alias-dialog'
import { usePrivateUserName } from './api'

export { PrivateUserAliasDialog } from './alias-dialog'

export function PrivateUserName(props: { userID: number; name: string }) {
  const name = usePrivateUserName(props.userID, props.name)
  return <span title={name}>{name}</span>
}

export function PrivateUserAliasButton(props: {
  userID: number
  name: string
  group?: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        variant='ghost'
        size='sm'
        aria-label={t('Set private alias for {{name}}', {
          name: props.name,
          interpolation: { escapeValue: false },
        })}
        onClick={() => setOpen(true)}
      >
        <Pencil className='size-3.5' aria-hidden='true' />
        {t('Private alias')}
      </Button>
      {open && (
        <PrivateUserAliasDialog {...props} open={open} onOpenChange={setOpen} />
      )}
    </>
  )
}
