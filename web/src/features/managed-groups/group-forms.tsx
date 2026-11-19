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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import { groupChange, groupPath } from './api'

const joinSchema = z.object({ group: z.string().trim().min(1).max(64) })
const channelSchema = z.object({
  name: z.string().trim().min(1).max(128),
  base_url: z.url().startsWith('https://'),
  key: z.string().min(1).max(65536),
  models: z.string().trim().min(1),
})

export function GroupJoinForm(props: { onSuccess?: () => void }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm({
    resolver: zodResolver(joinSchema),
    defaultValues: { group: '' },
  })
  const mutation = useMutation({
    mutationFn: (data: z.infer<typeof joinSchema>) =>
      groupChange({ method: 'POST', url: '/api/managed-groups/join', data }),
    onSuccess: () => {
      toast.success(t('Membership request sent'))
      void client.invalidateQueries({ queryKey: ['managed-groups'] })
      form.reset()
      props.onSuccess?.()
    },
  })
  return (
    <Form {...form}>
      <form
        className='flex flex-wrap items-end gap-3'
        onSubmit={form.handleSubmit((data) => mutation.mutate(data))}
      >
        <FormField
          control={form.control}
          name='group'
          render={({ field }) => (
            <FormItem className='min-w-48 flex-1'>
              <FormLabel>{t('Request to join a group')}</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('Group name provided by your manager')}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type='submit' variant='outline' disabled={mutation.isPending}>
          {t('Request membership')}
        </Button>
        <p className='text-muted-foreground w-full text-sm'>
          {t(
            'Joining changes your user group and its access and billing rules. Your account balance is preserved.'
          )}
        </p>
      </form>
    </Form>
  )
}

export function GroupChannelForm(props: {
  group: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm({
    resolver: zodResolver(channelSchema),
    defaultValues: { name: '', base_url: '', key: '', models: '' },
  })
  const mutation = useMutation({
    mutationFn: (data: z.infer<typeof channelSchema>) =>
      groupChange({
        method: 'POST',
        url: `${groupPath(props.group)}/channels`,
        data,
      }),
    onSuccess: () => {
      toast.success(t('Channel submitted for review'))
      form.reset()
      props.onOpenChange(false)
      void client.invalidateQueries({ queryKey: ['managed-groups'] })
    },
  })
  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open) form.reset()
        props.onOpenChange(open)
      }}
      title={t('Create dedicated group channel')}
      description={t(
        'OpenAI-compatible channels belong only to this group. A super administrator reviews the upstream and publishes prices before activation.'
      )}
    >
      <Form {...form}>
        <form
          className='space-y-4'
          onSubmit={form.handleSubmit((data) => mutation.mutate(data))}
        >
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Channel name')}</FormLabel>
                <FormControl>
                  <Input {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='base_url'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API Base URL')}</FormLabel>
                <FormControl>
                  <Input {...field} placeholder='https://api.example.com' />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='key'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API Key')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='password'
                    autoComplete='new-password'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='models'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Models (comma-separated)')}</FormLabel>
                <FormControl>
                  <Textarea {...field} rows={4} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <div className='flex justify-end'>
            <Button disabled={mutation.isPending} type='submit'>
              {t('Submit channel')}
            </Button>
          </div>
        </form>
      </Form>
    </Dialog>
  )
}
