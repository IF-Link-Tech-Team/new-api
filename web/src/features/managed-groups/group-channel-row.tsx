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
import { ChevronDown, ChevronUp } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTableRowActionMenu } from '@/components/data-table'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  adminGroupPath,
  groupPath,
  type groupChange,
  type GroupChannel,
  type ManagedGroup,
} from './api'

export function GroupChannelRow(props: {
  channel: GroupChannel
  group: ManagedGroup
  root: boolean
  pending: boolean
  onChange: (input: Parameters<typeof groupChange>[0]) => void
  onRevoke: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [expanded, setExpanded] = useState(false)
  const modelID = useId()
  const channel = props.channel
  const models = [
    ...new Set(
      channel.models
        .split(',')
        .map((name) => name.trim())
        .filter(Boolean)
    ),
  ]
  const active = channel.owned ? channel.status === 1 : channel.attached
  const needsReview = channel.owned && !channel.reviewed
  const canToggle = channel.owned
    ? props.group.permissions.includes('channels.manage') &&
      (channel.reviewed || active)
    : props.group.permissions.includes('channels.attach') &&
      channel.available &&
      (!channel.attached || channel.can_detach)
  let status = active ? t('Enabled') : t('Not enabled')
  if (!channel.owned) {
    status = channel.attached ? t('Attached') : t('Available to attach')
  }
  if (!channel.owned && channel.attached && channel.status !== 1) {
    status = t('Attached; upstream disabled')
  }
  if (needsReview) status = t('Pending review and pricing')
  let statusVariant: StatusVariant = active ? 'success' : 'neutral'
  if (
    needsReview ||
    (!channel.owned && channel.attached && channel.status !== 1)
  ) {
    statusVariant = 'warning'
  }
  return (
    <li className='py-4'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0 flex-1 space-y-2'>
          <div className='flex flex-wrap items-center gap-x-3 gap-y-1'>
            <h4 className='font-medium break-words'>{channel.name}</h4>
            <StatusBadge
              label={status}
              type='text'
              variant={statusVariant}
              copyable={false}
            />
          </div>
          <p className='text-muted-foreground text-xs'>
            {channel.owned ? t('Dedicated') : t('Shared')} · #{channel.id} ·{' '}
            {t('{{count}} models', {
              count: formatNumber(models.length, locale),
            })}
          </p>
        </div>
        <div className='flex shrink-0 items-center gap-1'>
          {props.root && needsReview && (
            <Button
              variant='outline'
              size='sm'
              disabled={props.pending}
              onClick={() =>
                props.onChange({
                  method: 'POST',
                  url: `${adminGroupPath(props.group.name)}/channels/${channel.id}/approve`,
                })
              }
            >
              {t('Approve channel after pricing')}
            </Button>
          )}
          {canToggle && (
            <Button
              variant='outline'
              size='sm'
              disabled={props.pending}
              onClick={() =>
                props.onChange({
                  method: 'PATCH',
                  url: `${groupPath(props.group.name)}/channels/${channel.id}`,
                  data: { enabled: !active },
                })
              }
            >
              {active
                ? t('Disable for this group')
                : t('Enable for this group')}
            </Button>
          )}
          {props.root && channel.available && (
            <DataTableRowActionMenu
              ariaLabel={t('More actions for {{name}}', { name: channel.name })}
            >
              <DropdownMenuItem
                onClick={props.onRevoke}
                disabled={props.pending}
              >
                {t('Revoke shared channel access')}
              </DropdownMenuItem>
            </DataTableRowActionMenu>
          )}
        </div>
      </div>
      <Button
        variant='ghost'
        size='sm'
        className='text-muted-foreground mt-2 h-7 gap-1 px-0 text-xs hover:bg-transparent'
        aria-expanded={expanded}
        aria-controls={modelID}
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? t('Hide models') : t('Show models')}
        {expanded ? (
          <ChevronUp className='size-3' aria-hidden='true' />
        ) : (
          <ChevronDown className='size-3' aria-hidden='true' />
        )}
      </Button>
      {expanded && (
        <div
          id={modelID}
          className='bg-muted/40 mt-2 flex max-h-48 flex-wrap gap-2 overflow-y-auto rounded-lg p-3'
          role='region'
          aria-label={t('Models for {{name}}', { name: channel.name })}
        >
          {models.map((model) => (
            <span
              key={model}
              className='bg-background max-w-full rounded border px-2 py-1 font-mono text-xs break-all'
            >
              {model}
            </span>
          ))}
        </div>
      )}
    </li>
  )
}
