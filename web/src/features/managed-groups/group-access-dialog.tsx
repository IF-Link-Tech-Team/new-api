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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PermissionMatrix } from '@/components/permission-matrix'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import type {
  AdminPermissionMatrix,
  PermissionResourceDef,
} from '@/lib/admin-permissions'

import { adminGroupPath, groupChange, groupGet } from './api'

export function GroupAccessDialog(props: {
  group: string
  open: boolean
  onOpenChange: (open: boolean) => void
  catalog: { key: string; label: string }[]
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const verification = useSecureVerification()
  const [input, setInput] = useState('')
  const [userID, setUserID] = useState(0)
  const [permissions, setPermissions] = useState<string[]>([])
  const query = useQuery({
    queryKey: ['managed-groups', props.group, 'grants', userID],
    queryFn: () =>
      groupGet<{ permission: string }[]>(
        `${adminGroupPath(props.group)}/grants/${userID}`
      ),
    enabled: props.open && userID > 0,
  })
  useEffect(() => {
    setPermissions(query.data?.map((row) => row.permission) ?? [])
  }, [query.data])
  const mutation = useMutation({
    mutationFn: (proof: string) =>
      groupChange({
        method: 'PUT',
        url: `${adminGroupPath(props.group)}/grants/${userID}`,
        data: { permissions },
        proof,
      }),
    onSuccess: () => {
      toast.success(t('Group permissions saved'))
      void client.invalidateQueries({ queryKey: ['managed-groups'] })
      props.onOpenChange(false)
    },
  })
  async function save() {
    const proof = await verification.requestVerification({
      scope: 'admin.user.update',
      context: { user_id: userID },
      title: t('Verify group authorization'),
      description: t(
        'Confirm your identity before changing group management permissions.'
      ),
    })
    if (proof) mutation.mutate(proof.proof_token)
  }
  const resources: PermissionResourceDef[] = []
  const values: AdminPermissionMatrix = {}
  for (const item of props.catalog) {
    const [resource, action] = item.key.split('.')
    const labels: Record<string, [string, string]> = {
      members: [
        'Group members',
        'Only ordinary members of this group can be managed.',
      ],
      wallet: [
        'Group balance',
        'Only balance allocated to this group can be managed.',
      ],
      channels: [
        'Group channels',
        'Shared channel settings and prices remain unchanged.',
      ],
    }
    const [label, description] = labels[resource] ?? labels.channels
    let definition = resources.find((entry) => entry.resource === resource)
    if (!definition) {
      definition = {
        resource,
        label_key: label,
        actions: [],
      }
      resources.push(definition)
    }
    definition.actions.push({
      action,
      label_key: item.label,
      description_key: description,
    })
    values[resource] = {
      ...values[resource],
      [action]: permissions.includes(item.key),
    }
  }
  const changed =
    [...permissions].sort().join(',') !==
    (query.data ?? [])
      .map((row) => row.permission)
      .sort()
      .join(',')
  let selectionMessage = t(
    'Changes take effect after you save and verify your identity.'
  )
  if (permissions.length === 0) {
    selectionMessage = t('Select only the permissions this user needs.')
    if ((query.data?.length ?? 0) > 0) {
      selectionMessage = t(
        'Saving with no permissions revokes access to this group.'
      )
    }
  }
  return (
    <>
      <Dialog
        open={props.open && !verification.isActive}
        onOpenChange={props.onOpenChange}
        title={t('Group authorization')}
        description={t('Permissions apply only to {{group}}.', {
          group: props.group,
        })}
        footer={
          <>
            <Button variant='outline' onClick={() => props.onOpenChange(false)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={
                !query.isSuccess ||
                userID <= 0 ||
                query.isFetching ||
                mutation.isPending ||
                !changed
              }
              onClick={() => void save()}
            >
              {t('Save permissions')}
            </Button>
          </>
        }
      >
        <div className='space-y-5'>
          <div className='flex items-end gap-3'>
            <div className='flex-1 space-y-2'>
              <Label htmlFor='group-grant-user'>{t('User ID')}</Label>
              <Input
                id='group-grant-user'
                type='number'
                min={1}
                value={input}
                onChange={(event) => {
                  setInput(event.target.value)
                  setUserID(0)
                }}
                onKeyDown={(event) => {
                  if (
                    event.key === 'Enter' &&
                    Number.isSafeInteger(Number(input)) &&
                    Number(input) > 0
                  ) {
                    setUserID(Number(input))
                  }
                }}
              />
            </div>
            <Button
              variant='outline'
              disabled={
                !Number.isSafeInteger(Number(input)) || Number(input) <= 0
              }
              onClick={() => setUserID(Number(input))}
            >
              {t('Load permissions')}
            </Button>
          </div>
          {query.isFetching && userID > 0 && <LoadingState />}
          {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
          {query.isSuccess && userID > 0 && (
            <>
              <div className='flex items-center justify-between gap-3'>
                <h3 className='text-sm font-medium'>
                  {t('Permissions for user {{id}}', { id: userID })}
                </h3>
                <Button
                  variant='ghost'
                  size='sm'
                  disabled={permissions.length === 0 || mutation.isPending}
                  onClick={() => setPermissions([])}
                >
                  {t('Clear')}
                </Button>
              </div>
              <PermissionMatrix
                resources={resources}
                value={values}
                disabled={mutation.isPending}
                onChange={(next) =>
                  setPermissions(
                    props.catalog
                      .filter((item) => {
                        const [resource, action] = item.key.split('.')
                        return next[resource]?.[action]
                      })
                      .map((item) => item.key)
                  )
                }
              />
              <p className='text-muted-foreground text-sm'>
                {selectionMessage}
              </p>
            </>
          )}
        </div>
      </Dialog>
      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}
