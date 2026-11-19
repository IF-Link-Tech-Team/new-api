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
import { PlugZap, Plus, Search } from 'lucide-react'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  adminGroupPath,
  groupChange,
  groupGet,
  groupPath,
  type GroupChannel,
  type ChannelCatalogItem,
  type ManagedGroup,
} from './api'
import { GroupChannelRow } from './group-channel-row'
import { GroupChannelForm } from './group-forms'

export function GroupChannelsPanel(props: {
  group: ManagedGroup
  root: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [create, setCreate] = useState(false)
  const [offerOpen, setOfferOpen] = useState(false)
  const [offerID, setOfferID] = useState('')
  const [search, setSearch] = useState('')
  const [revoke, setRevoke] = useState<GroupChannel | null>(null)
  const catalog = useQuery({
    queryKey: ['managed-groups', props.group.name, 'catalog'],
    queryFn: () =>
      groupGet<ChannelCatalogItem[]>(
        `/api/group-admin/channels?group=${encodeURIComponent(props.group.name)}`
      ),
    enabled: props.root && offerOpen,
  })
  const query = useQuery({
    queryKey: ['managed-groups', props.group.name, 'channels'],
    queryFn: () =>
      groupGet<GroupChannel[]>(`${groupPath(props.group.name)}/channels`),
  })
  const mutation = useMutation({
    mutationFn: groupChange,
    onSuccess: () => {
      setOfferOpen(false)
      setOfferID('')
      setRevoke(null)
      toast.success(t('Saved'))
      void client.invalidateQueries({ queryKey: ['managed-groups'] })
    },
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  const needle = search.trim().toLowerCase()
  const channels = query.data.filter((channel) =>
    `${channel.name} ${channel.models}`.toLowerCase().includes(needle)
  )
  return (
    <section className='space-y-5' aria-label={t('Group channels')}>
      <div className='flex flex-wrap items-start justify-between gap-4'>
        <div className='space-y-1'>
          <h3 className='flex items-center gap-2 font-semibold'>
            {t('Group channels')}
            <Badge variant='secondary'>
              {formatNumber(query.data.length, locale)}
            </Badge>
          </h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Shared channels keep their existing prices, credentials and other groups unchanged.'
            )}
          </p>
        </div>
        <div className='flex flex-wrap gap-2'>
          {props.root && (
            <Button
              size='sm'
              variant='outline'
              onClick={() => setOfferOpen(true)}
            >
              <PlugZap className='size-4' aria-hidden='true' />
              {t('Shared channel access')}
            </Button>
          )}
          {props.group.permissions.includes('channels.create') && (
            <Button size='sm' onClick={() => setCreate(true)}>
              <Plus className='size-4' aria-hidden='true' />
              {t('Create dedicated group channel')}
            </Button>
          )}
        </div>
      </div>
      {query.data.length > 0 && (
        <div className='relative max-w-sm'>
          <Search
            className='text-muted-foreground pointer-events-none absolute top-2.5 left-3 size-4'
            aria-hidden='true'
          />
          <Input
            className='pl-9'
            aria-label={t('Search channels or models')}
            placeholder={t('Search channels or models')}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
      )}
      {query.data.length === 0 && (
        <EmptyState icon={PlugZap} title={t('No group channels')} />
      )}
      {query.data.length > 0 && channels.length === 0 && (
        <EmptyState
          icon={Search}
          title={t('No matching channels')}
          action={
            <Button variant='outline' onClick={() => setSearch('')}>
              {t('Clear search')}
            </Button>
          }
        />
      )}
      {channels.length > 0 && (
        <ul className='divide-y border-y'>
          {channels.map((channel) => (
            <GroupChannelRow
              key={channel.id}
              channel={channel}
              group={props.group}
              root={props.root}
              pending={mutation.isPending}
              onChange={(input) => mutation.mutate(input)}
              onRevoke={() => setRevoke(channel)}
            />
          ))}
        </ul>
      )}
      <Dialog
        open={offerOpen}
        onOpenChange={setOfferOpen}
        title={t('Shared channel access')}
        description={t(
          'Choose an existing channel to make it available to this group. Its global configuration stays unchanged.'
        )}
        footer={
          <>
            <Button variant='outline' onClick={() => setOfferOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={mutation.isPending || !offerID || !catalog.isSuccess}
              onClick={() =>
                mutation.mutate({
                  method: 'PUT',
                  url: `${adminGroupPath(props.group.name)}/channels/${Number(offerID)}`,
                  data: { allowed: true },
                })
              }
            >
              {t('Allow group to attach')}
            </Button>
          </>
        }
      >
        {catalog.isPending && <LoadingState />}
        {catalog.isError && (
          <ErrorState onRetry={() => void catalog.refetch()} />
        )}
        {catalog.isSuccess && catalog.data.length === 0 && (
          <EmptyState title={t('No shareable channels')} />
        )}
        {catalog.isSuccess && catalog.data.length > 0 && (
          <div className='space-y-2'>
            <Label htmlFor='offer-channel'>{t('Shared channel')}</Label>
            <Select
              value={offerID}
              onValueChange={(value) => {
                if (value) setOfferID(value)
              }}
            >
              <SelectTrigger id='offer-channel' className='w-full'>
                <SelectValue placeholder={t('Select a channel')} />
              </SelectTrigger>
              <SelectContent>
                {catalog.data.map((channel) => (
                  <SelectItem key={channel.id} value={String(channel.id)}>
                    {channel.name} · #{channel.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
      </Dialog>
      <ConfirmDialog
        open={revoke !== null}
        onOpenChange={(open) => {
          if (!open) setRevoke(null)
        }}
        title={t('Revoke shared channel access')}
        desc={t(
          'This removes {{name}} from this group only. Other groups remain unchanged.',
          { name: revoke?.name }
        )}
        destructive
        confirmText={t('Revoke')}
        isLoading={mutation.isPending}
        handleConfirm={() => {
          if (revoke) {
            mutation.mutate({
              method: 'PUT',
              url: `${adminGroupPath(props.group.name)}/channels/${revoke.id}`,
              data: { allowed: false },
            })
          }
        }}
      />
      <GroupChannelForm
        group={props.group.name}
        open={create}
        onOpenChange={setCreate}
      />
    </section>
  )
}
