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
import {
  FolderClosed,
  PlugZap,
  ShieldCheck,
  Settings2,
  Activity,
  UserPlus,
  Users,
  Wallet,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { groupGet, type GroupOverview } from './api'
import { GroupAccessDialog } from './group-access-dialog'
import { GroupJoinForm } from './group-forms'
import { GroupModelSettings } from './group-model-settings'
import { GroupUsagePanel } from './group-usage-panel'
import { GroupWalletPanel, OwnGroupBalance } from './group-wallet-panel'
import { GroupChannelsPanel, GroupMembersPanel } from './group-workspace'

export function ManagedGroups() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const root = user?.role === ROLE.SUPER_ADMIN
  const [selection, setSelection] = useState('')
  const [grantOpen, setGrantOpen] = useState(false)
  const [joinOpen, setJoinOpen] = useState(false)
  const [section, setSection] = useState('members')
  const query = useQuery({
    queryKey: ['managed-groups', 'overview'],
    queryFn: () => groupGet<GroupOverview>('/api/managed-groups'),
  })
  const group =
    query.data?.groups.find((item) => item.name === selection) ??
    query.data?.groups[0]
  const canReadMembers = group?.permissions.some((permission) =>
    permission.startsWith('members.')
  )
  const canReadChannels = group?.permissions.some((permission) =>
    permission.startsWith('channels.')
  )
  const canReadWallet =
    root ||
    group?.permissions.some((permission) => permission.startsWith('wallet.'))
  const sections = [
    canReadMembers && 'members',
    canReadChannels && 'channels',
    canReadWallet && 'wallet',
    canReadWallet && 'usage',
    canReadChannels && 'settings',
  ].filter(Boolean)
  const activeSection = sections.includes(section)
    ? section
    : String(sections[0] || 'members')
  const tabClass = (value: string) =>
    `gap-2 rounded-none border-b-2 px-1 after:hidden ${activeSection === value ? 'border-b-primary text-primary' : 'border-b-transparent'}`
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('My groups')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {user?.role === ROLE.USER && (
          <Button size='sm' variant='outline' onClick={() => setJoinOpen(true)}>
            <UserPlus aria-hidden='true' className='size-4' />
            {t('Join a group')}
          </Button>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.data && (
          <div className='mx-auto w-full max-w-6xl space-y-6 pb-10'>
            {!root && <OwnGroupBalance />}
            {user?.role === ROLE.USER && query.data.join_request.group_name && (
              <p className='border-warning/25 bg-warning/5 rounded-lg border px-4 py-3 text-sm'>
                {t('Awaiting membership approval for {{group}}', {
                  group: query.data.join_request.group_name,
                })}
              </p>
            )}
            {group ? (
              <>
                <div className='flex flex-wrap items-center justify-between gap-5 border-b pb-6'>
                  <div className='flex min-w-0 items-center gap-4'>
                    <div className='bg-primary/8 text-primary flex size-12 shrink-0 items-center justify-center rounded-2xl'>
                      <FolderClosed className='size-6' aria-hidden='true' />
                    </div>
                    <div className='min-w-0 space-y-1'>
                      <h3 className='text-2xl font-semibold tracking-tight break-words'>
                        {group.name}
                      </h3>
                      <Badge variant='secondary'>
                        {group.mode === 'independent'
                          ? t('Independent group')
                          : t('Prepaid allocation group')}
                      </Badge>
                      <p className='text-muted-foreground text-sm'>
                        {t('Management permissions apply to this group only.')}
                      </p>
                    </div>
                  </div>
                  <div className='flex flex-wrap items-center gap-2'>
                    {query.data.groups.length > 1 && (
                      <Select
                        value={group.name}
                        onValueChange={(value) => {
                          if (value) setSelection(value)
                        }}
                      >
                        <SelectTrigger
                          className='w-44 max-w-full'
                          aria-label={t('Managed group')}
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {query.data.groups.map((item) => (
                            <SelectItem key={item.name} value={item.name}>
                              {item.name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                    {root && (
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => setGrantOpen(true)}
                      >
                        <ShieldCheck aria-hidden='true' className='size-4' />
                        {t('Group authorization')}
                      </Button>
                    )}
                  </div>
                </div>
                <Tabs
                  value={activeSection}
                  onValueChange={(value) => {
                    if (typeof value === 'string') setSection(value)
                  }}
                  className='gap-6'
                >
                  <TabsList
                    variant='line'
                    className='min-h-10 max-w-full flex-wrap gap-x-5 gap-y-1 border-b group-data-horizontal/tabs:h-auto'
                  >
                    {canReadWallet && (
                      <TabsTrigger
                        value='wallet'
                        className={tabClass('wallet')}
                      >
                        <Wallet className='size-4' aria-hidden='true' />
                        {t('Group balance')}
                      </TabsTrigger>
                    )}
                    {canReadMembers && (
                      <TabsTrigger
                        value='members'
                        className={tabClass('members')}
                      >
                        <Users className='size-4' aria-hidden='true' />
                        {t('Group members')}
                      </TabsTrigger>
                    )}
                    {canReadChannels && (
                      <TabsTrigger
                        value='channels'
                        className={tabClass('channels')}
                      >
                        <PlugZap className='size-4' aria-hidden='true' />
                        {t('Group channels')}
                      </TabsTrigger>
                    )}
                    {canReadWallet && (
                      <TabsTrigger value='usage' className={tabClass('usage')}>
                        <Activity className='size-4' aria-hidden='true' />
                        {t('Group usage')}
                      </TabsTrigger>
                    )}
                    {canReadChannels && (
                      <TabsTrigger
                        value='settings'
                        className={tabClass('settings')}
                      >
                        <Settings2 className='size-4' aria-hidden='true' />
                        {t('Group settings')}
                      </TabsTrigger>
                    )}
                  </TabsList>
                  {canReadWallet && activeSection === 'usage' && (
                    <TabsContent value='usage'>
                      <GroupUsagePanel
                        key={`${group.name}-usage`}
                        group={group.name}
                      />
                    </TabsContent>
                  )}
                  {canReadChannels && activeSection === 'settings' && (
                    <TabsContent value='settings'>
                      <GroupModelSettings
                        key={`${group.name}-settings`}
                        group={group}
                      />
                    </TabsContent>
                  )}
                  {canReadWallet && activeSection === 'wallet' && (
                    <TabsContent value='wallet'>
                      <GroupWalletPanel
                        key={`${group.name}-wallet`}
                        group={group}
                        root={root}
                      />
                    </TabsContent>
                  )}
                  {canReadMembers && activeSection === 'members' && (
                    <TabsContent value='members'>
                      <GroupMembersPanel
                        key={`${group.name}-members`}
                        group={group}
                      />
                    </TabsContent>
                  )}
                  {canReadChannels && activeSection === 'channels' && (
                    <TabsContent value='channels'>
                      <GroupChannelsPanel
                        key={`${group.name}-channels`}
                        group={group}
                        root={root}
                      />
                    </TabsContent>
                  )}
                </Tabs>
                {root && (
                  <GroupAccessDialog
                    key={group.name}
                    group={group.name}
                    open={grantOpen}
                    onOpenChange={setGrantOpen}
                    catalog={query.data.permissions}
                  />
                )}
              </>
            ) : (
              <EmptyState
                title={t('No managed groups')}
                description={t(
                  'A super administrator can grant management permissions for a specific group.'
                )}
              />
            )}
            {user?.role === ROLE.USER && (
              <Dialog
                open={joinOpen}
                onOpenChange={setJoinOpen}
                title={t('Join a group')}
              >
                <GroupJoinForm onSuccess={() => setJoinOpen(false)} />
              </Dialog>
            )}
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
