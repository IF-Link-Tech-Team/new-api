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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCheck, Clock3, RefreshCw, Users } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import {
  PrivateUserAliasButton,
  PrivateUserName,
} from '@/features/private-user-alias'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  groupChange,
  groupGet,
  groupPath,
  type GroupMember,
  type GroupMembers,
  type ManagedGroup,
} from './api'

export function GroupMembersPanel(props: { group: ManagedGroup }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const [remove, setRemove] = useState<GroupMember | null>(null)
  const query = useQuery({
    queryKey: ['managed-groups', props.group.name, 'members', page],
    queryFn: () =>
      groupGet<GroupMembers>(
        `${groupPath(props.group.name)}/members?page=${page}`
      ),
  })
  const mutation = useMutation({
    mutationFn: groupChange,
    onSuccess: () => {
      setRemove(null)
      toast.success(t('Group members updated'))
      void client.invalidateQueries({ queryKey: ['managed-groups'] })
    },
  })
  const canManage = props.group.permissions.includes('members.manage')
  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  return (
    <section className='space-y-5' aria-label={t('Group members')}>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <p className='text-muted-foreground max-w-xl text-sm leading-relaxed'>
          {t('Members can request to join {{group}} from My groups.', {
            group: props.group.name,
          })}
        </p>
        <div className='flex shrink-0 items-center gap-2'>
          <CopyButton
            value={props.group.name}
            size='sm'
            variant='outline'
            aria-label={t('Copy group name')}
          >
            {t('Copy group name')}
          </CopyButton>
          <Button
            variant='ghost'
            size='sm'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            <RefreshCw className='size-4' aria-hidden='true' />
            {t('Refresh')}
          </Button>
        </div>
      </div>
      <div
        className={
          canManage
            ? 'grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,0.65fr)]'
            : ''
        }
      >
        {canManage && (
          <Card className='min-w-0 lg:order-2'>
            <CardHeader className='border-b'>
              <h3 className='flex items-center gap-2 font-semibold'>
                <Clock3
                  className='text-muted-foreground size-4'
                  aria-hidden='true'
                />
                {t('Pending membership requests')}
                <Badge
                  variant={query.data.requests.length ? 'default' : 'secondary'}
                >
                  {formatNumber(query.data.requests.length, locale)}
                </Badge>
              </h3>
              <p className='text-muted-foreground text-sm'>
                {t('Approval grants access to this group only.')}
              </p>
            </CardHeader>
            <CardContent>
              <section aria-label={t('Pending membership requests')}>
                {query.data.requests.length === 0 ? (
                  <EmptyState
                    icon={CheckCheck}
                    title={t('No pending membership requests')}
                    description={t('New requests will appear here for review.')}
                    className='min-h-56'
                  />
                ) : (
                  <ul className='divide-y'>
                    {query.data.requests.map((member) => (
                      <li
                        key={member.id}
                        className='space-y-3 py-4 first:pt-0 last:pb-0'
                      >
                        <div className='min-w-0'>
                          <p className='font-medium break-words'>
                            <PrivateUserName
                              userID={member.id}
                              name={member.display_name || member.username}
                            />
                          </p>
                          <p className='text-muted-foreground text-xs break-all'>
                            {member.username} · #{member.id}
                          </p>
                        </div>
                        <div className='flex flex-wrap gap-2'>
                          <Button
                            size='sm'
                            disabled={mutation.isPending}
                            onClick={() =>
                              mutation.mutate({
                                method: 'POST',
                                url: `${groupPath(props.group.name)}/requests/${member.id}`,
                                data: { approve: true },
                              })
                            }
                          >
                            {t('Approve')}
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={mutation.isPending}
                            onClick={() =>
                              mutation.mutate({
                                method: 'POST',
                                url: `${groupPath(props.group.name)}/requests/${member.id}`,
                                data: { approve: false },
                              })
                            }
                          >
                            {t('Reject')}
                          </Button>
                          <PrivateUserAliasButton
                            userID={member.id}
                            name={member.display_name || member.username}
                            group={props.group.name}
                          />
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </CardContent>
          </Card>
        )}
        <Card className='min-w-0 lg:order-1'>
          <CardHeader className='border-b'>
            <h3 className='flex items-center gap-2 font-semibold'>
              <Users
                className='text-muted-foreground size-4'
                aria-hidden='true'
              />
              {t('Group members')}
              <Badge variant='secondary'>
                {formatNumber(query.data.total, locale)}
              </Badge>
            </h3>
            <p className='text-muted-foreground text-sm'>
              {t('Only ordinary members of this group can be managed.')}
            </p>
          </CardHeader>
          <CardContent>
            {query.data.items.length === 0 ? (
              <EmptyState
                icon={Users}
                title={t('No group members')}
                description={t('Approved applicants will appear here.')}
                className='min-h-56'
              />
            ) : (
              <ul className='divide-y'>
                {query.data.items.map((member) => (
                  <li
                    key={member.id}
                    className='flex flex-wrap items-center justify-between gap-3 py-4 first:pt-0 last:pb-0'
                  >
                    <div className='flex min-w-0 flex-1 items-center gap-3'>
                      <div
                        className='bg-muted text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-full text-sm font-medium'
                        aria-hidden='true'
                      >
                        {[
                          ...(member.display_name || member.username),
                        ][0]?.toUpperCase()}
                      </div>
                      <div className='min-w-0'>
                        <p className='font-medium break-words'>
                          <PrivateUserName
                            userID={member.id}
                            name={member.display_name || member.username}
                          />
                        </p>
                        <p className='text-muted-foreground text-xs break-all'>
                          {member.username} · #{member.id}
                        </p>
                        <StatusBadge
                          label={
                            member.status === 1 ? t('Enabled') : t('Disabled')
                          }
                          variant={member.status === 1 ? 'success' : 'neutral'}
                          type='text'
                          copyable={false}
                          className='mt-1'
                        />
                      </div>
                    </div>
                    <div className='flex flex-wrap gap-2'>
                      <PrivateUserAliasButton
                        userID={member.id}
                        name={member.display_name || member.username}
                        group={props.group.name}
                      />
                      {canManage && props.group.name !== 'default' && (
                        <Button
                          variant='ghost'
                          size='sm'
                          disabled={mutation.isPending}
                          onClick={() => setRemove(member)}
                        >
                          {t('Remove from group')}
                        </Button>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
            {(page > 1 || page * 50 < query.data.total) && (
              <div className='mt-5 flex flex-wrap items-center justify-between gap-3 border-t pt-4'>
                <span className='text-muted-foreground text-sm'>
                  {t('Page {{page}}', { page: formatNumber(page, locale) })}
                </span>
                <div className='flex gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page === 1 || query.isFetching}
                    onClick={() => setPage(page - 1)}
                  >
                    {t('Previous')}
                  </Button>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page * 50 >= query.data.total || query.isFetching}
                    onClick={() => setPage(page + 1)}
                  >
                    {t('Next')}
                  </Button>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
      <ConfirmDialog
        open={remove !== null}
        onOpenChange={(open) => {
          if (!open) setRemove(null)
        }}
        title={t('Remove {{name}} from {{group}}?', {
          name: remove?.display_name || remove?.username,
          group: props.group.name,
        })}
        desc={t(
          'The account returns to the default group. Its balance, credentials and usage history are preserved.'
        )}
        confirmText={t('Remove')}
        destructive
        isLoading={mutation.isPending}
        handleConfirm={() => {
          if (remove !== null) {
            mutation.mutate({
              method: 'DELETE',
              url: `${groupPath(props.group.name)}/members/${remove.id}`,
            })
          }
        }}
      />
    </section>
  )
}
