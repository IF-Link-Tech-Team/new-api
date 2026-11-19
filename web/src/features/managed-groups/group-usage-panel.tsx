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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { PrivateUserName } from '@/features/private-user-alias'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'

import { groupGet, groupPath } from './api'

type UsagePage = {
  items: {
    id: number
    request_id: string
    created_at: number
    user_id: number
    username: string
    model_name: string
    quota: number
    prompt_tokens: number
    completion_tokens: number
  }[]
  has_more: boolean
}
export function GroupUsagePanel(props: { group: string }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['managed-groups', props.group, 'usage', page],
    queryFn: () =>
      groupGet<UsagePage>(`${groupPath(props.group)}/usage?page=${page}`),
  })
  return (
    <section className='space-y-4' aria-label={t('Group usage')}>
      <div className='flex items-start justify-between gap-4'>
        <div>
          <h3 className='text-lg font-semibold'>{t('Group usage')}</h3>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'All recorded calls through this group, including free calls. Balance transfers are shown in Group balance.'
            )}
          </p>
        </div>
        <Button
          variant='outline'
          size='sm'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.data?.items.length === 0 && (
        <EmptyState title={t('No group usage yet')} />
      )}
      {!!query.data?.items.length && (
        <ul className='divide-y border-y'>
          {query.data.items.map((row) => (
            <li
              key={row.request_id || row.id}
              className='flex flex-wrap items-start justify-between gap-3 py-4'
            >
              <div className='min-w-0 space-y-1'>
                <p className='font-mono text-sm break-all'>{row.model_name}</p>
                <p className='text-muted-foreground text-sm'>
                  <PrivateUserName userID={row.user_id} name={row.username} /> ·{' '}
                  {new Date(row.created_at * 1000).toLocaleString(locale)}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {t('Input')} {formatNumber(row.prompt_tokens, locale)} ·{' '}
                  {t('Output')} {formatNumber(row.completion_tokens, locale)}
                </p>
              </div>
              <div className='text-right'>
                <p className='font-medium tabular-nums'>
                  {formatQuota(row.quota)}
                </p>
                {row.quota === 0 && (
                  <Badge variant='secondary'>{t('Free call')}</Badge>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className='flex justify-end gap-2'>
        <Button
          variant='outline'
          size='sm'
          disabled={page === 1 || query.isFetching}
          onClick={() => setPage(page - 1)}
        >
          {t('Previous page')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          disabled={!query.data?.has_more || query.isFetching}
          onClick={() => setPage(page + 1)}
        >
          {t('Next page')}
        </Button>
      </div>
    </section>
  )
}
