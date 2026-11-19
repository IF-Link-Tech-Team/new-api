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
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
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

import { savePrivateUserAlias, usePrivateUserAliases } from './api'

const aliasSchema = z.object({
  alias: z
    .string()
    .trim()
    .refine(
      (value) => [...value].length <= 64 && !/\p{Cc}/u.test(value),
      'Private aliases must be a single line of up to 64 characters'
    ),
})

type AliasDialogProps = {
  userID: number
  name: string
  group?: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function PrivateUserAliasDialog(props: AliasDialogProps) {
  const { t } = useTranslation()
  const query = usePrivateUserAliases()
  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Private alias')}
      description={t(
        'Only you see this alias. The original name and other users’ views stay unchanged.'
      )}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.isSuccess && (
        <PrivateAliasForm
          key={`${query.ownerID}:${props.userID}`}
          {...props}
          alias={query.data[props.userID] ?? ''}
          ownerID={query.ownerID}
        />
      )}
    </Dialog>
  )
}

function PrivateAliasForm(
  props: AliasDialogProps & { alias: string; ownerID?: number }
) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm({
    resolver: zodResolver(aliasSchema),
    defaultValues: { alias: props.alias },
  })
  const value = form.watch('alias').trim()
  const mutation = useMutation({
    mutationFn: (data: z.infer<typeof aliasSchema>) =>
      savePrivateUserAlias(props.userID, data.alias, props.group),
    onSuccess: (_result, data) => {
      client.setQueryData<Record<number, string>>(
        ['private-user-aliases', props.ownerID],
        (previous) => ({ ...previous, [props.userID]: data.alias })
      )
      toast.success(t('Private alias saved'))
      props.onOpenChange(false)
    },
  })
  return (
    <Form {...form}>
      <form
        className='space-y-5'
        onSubmit={form.handleSubmit((data) => mutation.mutate(data))}
      >
        <FormField
          control={form.control}
          name='alias'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('Private alias for {{name}}', {
                  name: props.name,
                  interpolation: { escapeValue: false },
                })}
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  autoFocus
                  autoComplete='off'
                  maxLength={128}
                  placeholder={t('Leave empty to remove the alias')}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <div className='bg-muted/40 rounded-lg p-4 text-sm break-words'>
          <p className='text-muted-foreground mb-1'>{t('Visible to you')}</p>
          <p className='font-medium'>
            {value
              ? t('{{name}} ({{alias}})', {
                  name: props.name,
                  alias: value,
                  interpolation: { escapeValue: false },
                })
              : props.name}
          </p>
        </div>
        <div className='flex flex-wrap justify-end gap-2'>
          <Button
            type='button'
            variant='ghost'
            disabled={mutation.isPending || !value}
            onClick={() => form.setValue('alias', '', { shouldValidate: true })}
          >
            {t('Clear alias')}
          </Button>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' disabled={mutation.isPending}>
            {t('Save alias')}
          </Button>
        </div>
      </form>
    </Form>
  )
}
