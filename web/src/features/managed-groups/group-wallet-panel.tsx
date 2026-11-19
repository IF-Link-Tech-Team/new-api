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
import { ArrowDownLeft, ArrowUpRight, Wallet, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  PrivateUserAliasButton,
  PrivateUserName,
} from '@/features/private-user-alias'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'

import {
  groupGet,
  groupChange,
  groupPath,
  adminGroupPath,
  type ManagedGroup,
} from './api'

type WalletData = {
  id: number
  group_name: string
  enabled: boolean
  activated: boolean
  quota: number
  mode: 'prepaid' | 'independent'
}
type Member = {
  user_id: number
  username: string
  display_name: string
  active: boolean
  quota: number
  used_quota: number
}
type Entry = {
  id: number
  user_id: number
  actor_id: number
  kind: string
  delta: number
  reason: string
  created_at: number
}
export type GroupWalletView = {
  wallet: WalletData
  allocated: number
  used: number
  members: Member[]
  total: number
  entries: Entry[]
  can_change_mode: boolean
}

export function OwnGroupBalance() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['managed-groups', 'own-wallet'],
    queryFn: () =>
      groupGet<{ wallet: WalletData | null; member: Member }>(
        '/api/managed-groups/wallet'
      ),
  })
  if (!query.data?.wallet?.activated) return null
  return (
    <Card>
      <CardContent className='flex flex-wrap items-center justify-between gap-4 py-5'>
        <div>
          <p className='text-muted-foreground text-sm'>
            {!query.data.wallet.enabled && (
              <Badge variant='secondary'>{t('Paused')}</Badge>
            )}{' '}
            {t('My balance in {{group}}', {
              group: query.data.wallet.group_name,
            })}
          </p>
          <p className='mt-1 text-3xl font-semibold tabular-nums'>
            {formatQuota(query.data.member.quota)}
          </p>
        </div>
        <p className='text-muted-foreground max-w-md text-sm'>
          {query.data.wallet.mode === 'independent'
            ? t(
                'Keys set to {{group}} use group credits. Normal-group keys use your personal balance or bound plan. Auto stays in {{group}}.',
                { group: query.data.wallet.group_name }
              )
            : t(
                'Only usable in this group. Ask your group manager for an allocation.'
              )}
        </p>
      </CardContent>
    </Card>
  )
}

export function GroupWalletPanel({
  group,
  root,
}: {
  group: ManagedGroup
  root: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const [changeMode, setChangeMode] = useState(false)
  const [action, setAction] = useState<{
    kind: 'fund' | 'reduce' | 'allocate' | 'reclaim'
    member?: Member
  } | null>(null)
  const [amount, setAmount] = useState('')
  const [operationID, setOperationID] = useState('')
  const [reason, setReason] = useState('')
  const query = useQuery({
    queryKey: ['managed-groups', group.name, 'wallet', page],
    queryFn: () =>
      groupGet<GroupWalletView>(`${groupPath(group.name)}/wallet?page=${page}`),
  })
  const canAllocate = root || group.permissions.includes('wallet.allocate')
  const mutation = useMutation({
    mutationFn: groupChange,
    onSuccess: () => {
      setAction(null)
      setAmount('')
      setReason('')
      setChangeMode(false)
      toast.success(t('Group balance updated'))
      void client.invalidateQueries({ queryKey: ['managed-groups'] })
    },
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  const data = query.data
  const independent = data.wallet.mode === 'independent'
  const canManage =
    root || (independent && group.permissions.includes('wallet.manage'))
  const walletPath = independent
    ? groupPath(group.name)
    : adminGroupPath(group.name)
  let pausedDescription = t(
    'Disabled groups keep existing billing. Only a super administrator can enable or fund the pool.'
  )
  if (data.wallet.activated) {
    pausedDescription = t(
      'Group wallet paused. New group-funded requests are blocked; personal wallets are not used instead.'
    )
  }
  if (independent) {
    pausedDescription = t(
      'Group credits are paused. A group credit manager can enable them; other balances will not be used.'
    )
  }
  const quantity = parseQuotaFromDollars(Number(amount))
  const validAmount = Number.isSafeInteger(quantity) && quantity > 0
  let limit: number | undefined
  if (action?.kind === 'allocate') limit = data.wallet.quota
  if (action?.kind === 'reclaim') limit = action.member?.quota
  if (action?.kind === 'reduce') limit = data.wallet.quota
  let walletStatus = t('Not enabled')
  if (data.wallet.activated) walletStatus = t('Paused')
  if (data.wallet.enabled) walletStatus = t('Enabled')
  let actionTitle = t('Allocate to member')
  if (action?.kind === 'fund') actionTitle = t('Fund group pool')
  if (action?.kind === 'fund' && independent) {
    actionTitle = t('Increase group credits')
  }
  if (action?.kind === 'reduce') actionTitle = t('Reduce group credits')
  if (action?.kind === 'reclaim') actionTitle = t('Reclaim member allocation')
  const entryLabels: Record<string, string> = {
    fund: independent ? 'Credit adjustment' : 'Pool funding',
    allocate: 'Member allocation',
    reserve: 'Request reservation',
    refund: 'Request refund',
    settle: 'Usage settlement',
  }
  const open = (
    kind: 'fund' | 'reduce' | 'allocate' | 'reclaim',
    member?: Member
  ) => {
    setAmount('')
    setReason('')
    setOperationID(crypto.randomUUID())
    setAction({ kind, member })
  }
  const save = () => {
    if (!action || !validAmount) return
    mutation.mutate({
      method: 'POST',
      url:
        action.kind === 'fund' || action.kind === 'reduce'
          ? `${walletPath}/wallet/funds`
          : `${groupPath(group.name)}/wallet/allocations/${action.member?.user_id}`,
      data: {
        delta:
          action.kind === 'reclaim' || action.kind === 'reduce'
            ? -quantity
            : quantity,
        reason,
        operation_id: operationID,
      },
    })
  }
  return (
    <section className='space-y-6' aria-label={t('Group balance')}>
      <div className='flex flex-wrap items-start justify-between gap-4'>
        <div>
          <div className='flex items-center gap-2'>
            <Wallet className='size-4' aria-hidden='true' />
            <h3 className='font-semibold'>{t('Group balance')}</h3>
            <Badge variant={data.wallet.enabled ? 'default' : 'secondary'}>
              {walletStatus}
            </Badge>
          </div>
          <p className='text-muted-foreground mt-2 max-w-2xl text-sm'>
            {independent
              ? t(
                  'The group manager issues and allocates credits. Credits stay in this group and cannot become personal balance.'
                )
              : t(
                  'Allocate the shared pool to members. Spending stays in this group and never draws from personal wallets.'
                )}
          </p>
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            <RefreshCw className='size-4' aria-hidden='true' />
            {t('Refresh')}
          </Button>
          {canManage && (
            <>
              <Button variant='outline' size='sm' onClick={() => open('fund')}>
                {independent
                  ? t('Increase group credits')
                  : t('Fund group pool')}
              </Button>
              {independent && (
                <Button
                  variant='outline'
                  size='sm'
                  disabled={data.wallet.quota <= 0}
                  onClick={() => open('reduce')}
                >
                  {t('Reduce group credits')}
                </Button>
              )}
              <Button
                variant='outline'
                size='sm'
                disabled={mutation.isPending}
                onClick={() =>
                  mutation.mutate({
                    method: 'PATCH',
                    url: `${walletPath}/wallet`,
                    data: { enabled: !data.wallet.enabled },
                  })
                }
              >
                {data.wallet.enabled
                  ? t('Pause group wallet')
                  : t('Enable group wallet')}
              </Button>
            </>
          )}
          {root && data.can_change_mode && (
            <Button
              variant='ghost'
              size='sm'
              onClick={() => setChangeMode(true)}
            >
              {t('Change group type')}
            </Button>
          )}
        </div>
      </div>
      <div className='grid grid-cols-2 gap-4 sm:grid-cols-3'>
        {[
          [
            independent ? t('Unallocated credits') : t('Unallocated pool'),
            data.wallet.quota,
          ],
          [t('Allocated remaining'), data.allocated],
          [t('Used, including reservations'), data.used],
        ].map(([label, value], index) => (
          <Card
            key={String(label)}
            className={index === 0 ? 'col-span-2 sm:col-span-1' : ''}
          >
            <CardContent className='py-4 sm:py-5'>
              <p className='text-muted-foreground text-sm'>{label}</p>
              <p className='mt-2 text-2xl font-semibold tabular-nums'>
                {formatQuota(Number(value))}
              </p>
            </CardContent>
          </Card>
        ))}
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'New requests need sufficient allocation. Requests already in progress settle their actual usage against the same group.'
        )}
      </p>
      {!data.wallet.enabled && (
        <p className='text-muted-foreground rounded-lg border border-dashed p-4 text-sm'>
          {pausedDescription}
        </p>
      )}
      <Card>
        <CardHeader className='border-b'>
          <h3 className='font-semibold'>{t('Member allocations')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Reclaim unused allocations before redistributing. Former members cannot spend their remaining allocation.'
            )}
          </p>
        </CardHeader>
        <CardContent className='p-0'>
          {data.members.length === 0 ? (
            <EmptyState
              title={t('No group members')}
              description={t(
                'Approve a member, then allocate balance from the pool.'
              )}
            />
          ) : (
            <div className='divide-y'>
              {data.members.map((member) => (
                <div
                  key={member.user_id}
                  className='flex flex-wrap items-center justify-between gap-4 px-5 py-4'
                >
                  <div className='min-w-0'>
                    <p className='font-medium break-words'>
                      <PrivateUserName
                        userID={member.user_id}
                        name={member.display_name || member.username}
                      />{' '}
                      <span className='text-muted-foreground text-xs font-normal'>
                        #{member.user_id}
                      </span>
                    </p>
                    <p className='text-muted-foreground mt-1 text-sm'>
                      {member.active
                        ? member.username
                        : t('Former or inactive member')}
                    </p>
                  </div>
                  <div className='ml-auto flex flex-wrap items-center gap-4'>
                    <PrivateUserAliasButton
                      userID={member.user_id}
                      name={member.display_name || member.username}
                      group={group.name}
                    />
                    <div className='text-right'>
                      <p className='font-medium tabular-nums'>
                        {formatQuota(member.quota)}
                      </p>
                      <p className='text-muted-foreground text-xs'>
                        {t('Used')}: {formatQuota(member.used_quota)}
                      </p>
                    </div>
                    {canAllocate && (
                      <div className='flex gap-2'>
                        <Button
                          variant='outline'
                          size='sm'
                          disabled={!member.active || data.wallet.quota <= 0}
                          onClick={() => open('allocate', member)}
                        >
                          <ArrowDownLeft
                            className='size-4'
                            aria-hidden='true'
                          />
                          {t('Allocate')}
                        </Button>
                        <Button
                          variant='ghost'
                          size='sm'
                          disabled={member.quota <= 0}
                          onClick={() => open('reclaim', member)}
                        >
                          <ArrowUpRight className='size-4' aria-hidden='true' />
                          {t('Reclaim')}
                        </Button>
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
          {data.total > 50 && (
            <div className='flex items-center justify-end gap-3 border-t p-4'>
              <Button
                variant='outline'
                size='sm'
                disabled={page === 1}
                onClick={() => setPage(page - 1)}
              >
                {t('Previous')}
              </Button>
              <span className='text-sm'>
                {page} / {Math.ceil(data.total / 50)}
              </span>
              <Button
                variant='outline'
                size='sm'
                disabled={page * 50 >= data.total}
                onClick={() => setPage(page + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader className='border-b'>
          <h3 className='font-semibold'>{t('Recent balance activity')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Latest 30 entries. Positive amounts credit the affected balance; negative amounts debit it.'
            )}
          </p>
        </CardHeader>
        <CardContent className='divide-y'>
          {data.entries.length === 0 ? (
            <p className='text-muted-foreground py-8 text-center text-sm'>
              {t('No balance activity yet')}
            </p>
          ) : (
            data.entries.map((entry) => (
              <div
                key={entry.id}
                className='flex items-start justify-between gap-4 py-3 text-sm'
              >
                <div>
                  <p className='font-medium'>
                    {t(entryLabels[entry.kind] ?? 'Usage settlement')}
                    {entry.user_id > 0 && ` · #${entry.user_id}`}
                  </p>
                  <p className='text-muted-foreground mt-1 text-xs break-words'>
                    {new Date(entry.created_at * 1000).toLocaleString()}
                    {entry.actor_id > 0 && ` · #${entry.actor_id}`}
                    {entry.reason && (
                      <span className='mt-1 block'>{entry.reason}</span>
                    )}
                  </p>
                </div>
                <span className='shrink-0 font-medium tabular-nums'>
                  {entry.delta > 0 ? '+' : ''}
                  {formatQuota(entry.delta)}
                </span>
              </div>
            ))
          )}
        </CardContent>
      </Card>
      <Dialog
        open={!!action}
        onOpenChange={(value) => {
          if (!value) setAction(null)
        }}
        title={actionTitle}
        description={
          action?.member
            ? `${group.name} · ${action.member.display_name || action.member.username} (#${action.member.user_id})`
            : group.name
        }
        footer={
          <>
            <Button variant='outline' onClick={() => setAction(null)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={
                !validAmount ||
                (limit !== undefined && quantity > limit) ||
                ((action?.kind === 'fund' || action?.kind === 'reduce') &&
                  !reason.trim()) ||
                mutation.isPending
              }
              onClick={save}
            >
              {t('Confirm')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='group-wallet-amount'>{t('Amount')}</Label>
            <Input
              id='group-wallet-amount'
              inputMode='decimal'
              type='number'
              min='0'
              step='0.01'
              value={amount}
              onChange={(event) => setAmount(event.target.value)}
            />
            <p className='text-muted-foreground text-sm'>
              {t('Balance change')}:{' '}
              {formatQuota(
                action?.kind === 'reduce' || action?.kind === 'reclaim'
                  ? -quantity
                  : quantity
              )}
              {limit !== undefined &&
                ` · ${t('Available')}: ${formatQuota(limit)}`}
            </p>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='group-wallet-reason'>{t('Reason')}</Label>
            <Input
              id='group-wallet-reason'
              maxLength={500}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
        </div>
      </Dialog>
      <ConfirmDialog
        open={changeMode}
        onOpenChange={setChangeMode}
        title={
          independent
            ? t('Use prepaid allocation')
            : t('Use independent credits')
        }
        desc={t(
          'Changing {{group}} affects its group access and funding rules. The type is locked after the first balance activity.',
          { group: group.name }
        )}
        confirmText={t('Change group type')}
        isLoading={mutation.isPending}
        handleConfirm={() =>
          mutation.mutate({
            method: 'PUT',
            url: `${adminGroupPath(group.name)}/wallet/type`,
            data: { mode: independent ? 'prepaid' : 'independent' },
          })
        }
      />
    </section>
  )
}
