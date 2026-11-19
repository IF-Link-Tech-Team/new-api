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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ManagedGroups } from '..'
import type { GroupMember } from '../api'
import { GroupAccessDialog } from '../group-access-dialog'
import { OwnGroupBalance } from '../group-wallet-panel'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('independent members see how separate keys choose group credits or normal billing', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        wallet: {
          id: 1,
          group_name: 'Astro',
          mode: 'independent',
          enabled: true,
          activated: true,
          quota: 0,
        },
        member: { quota: 500000 },
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <OwnGroupBalance />
    </QueryClientProvider>
  )
  expect(
    await screen.findByText(
      'Keys set to Astro use group credits. Normal-group keys use your personal balance or bound plan. Auto stays in Astro.'
    )
  ).toBeVisible()
  expect(
    screen.queryByText(
      'Only usable in this group. Ask your group manager for an allocation.'
    )
  ).not.toBeInTheDocument()
})

function renderWorkspace(
  permissions: string[],
  role = 1,
  requests: GroupMember[] = [],
  mode: 'prepaid' | 'independent' = 'prepaid'
) {
  useAuthStore.getState().auth.setUser({ id: 2, username: 'manager', role })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).startsWith('/api/group-admin/channels?')) {
      return {
        data: {
          success: true,
          data: [{ id: 12, name: 'Additional channel', group: 'default' }],
        },
      }
    }
    if (url === '/api/managed-groups') {
      return {
        data: {
          success: true,
          data: {
            groups: permissions.length
              ? [{ name: 'alpha', permissions, mode }]
              : [],
            permissions: [],
            join_request: {},
          },
        },
      }
    }
    if (url === '/api/managed-groups/wallet') {
      return {
        data: { success: true, data: { wallet: null, member: { quota: 0 } } },
      }
    }
    if (String(url).includes('/wallet?')) {
      return {
        data: {
          success: true,
          data: {
            wallet: {
              id: 1,
              group_name: 'alpha',
              enabled: true,
              activated: true,
              quota: 5000000,
              mode,
            },
            allocated: 2500000,
            used: 1000000,
            total: 1,
            members: [
              {
                user_id: 3,
                username: 'member',
                display_name: 'Member',
                active: true,
                quota: 2500000,
                used_quota: 1000000,
              },
            ],
            entries: [],
            can_change_mode: true,
          },
        },
      }
    }
    if (String(url).includes('/members')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              { id: 3, username: 'member', display_name: 'Member', status: 1 },
            ],
            total: 1,
            requests,
          },
        },
      }
    }
    if (String(url).endsWith('/channels')) {
      return {
        data: {
          success: true,
          data: [
            {
              id: 10,
              name: 'Shared channel',
              models: 'example-model',
              owned: false,
              available: true,
              attached: false,
              can_detach: false,
              status: 1,
            },
            {
              id: 11,
              name: 'Pending channel',
              models: 'new-model',
              owned: true,
              reviewed: false,
              attached: true,
              status: 2,
            },
          ],
        },
      }
    }
    if (String(url).endsWith('/models')) {
      return { data: { success: true, data: [] } }
    }
    if (String(url).includes('/usage?')) {
      return { data: { success: true, data: { items: [], has_more: false } } }
    }
    throw new Error(`Unexpected URL: ${url}`)
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ManagedGroups />
    </QueryClientProvider>
  )
}

it('read-only managers see their group without member or channel mutation controls', async () => {
  renderWorkspace(['members.read', 'channels.read'])
  await screen.findByText('Member')
  await userEvent.click(screen.getByRole('tab', { name: 'Group channels' }))
  await screen.findByText('Shared channel')
  expect(
    screen.queryByRole('button', { name: 'Remove from group' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Enable for this group' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Create dedicated group channel' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Group authorization' })
  ).not.toBeInTheDocument()
})

it('attach permission changes only the selected group binding and pending channels cannot be enabled', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace(['channels.attach', 'channels.manage'])
  await screen.findByText('Pending channel')
  const buttons = screen.getAllByRole('button', {
    name: 'Enable for this group',
  })
  expect(buttons).toHaveLength(1)
  await userEvent.click(buttons[0])
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'PATCH',
        url: '/api/managed-groups/alpha/channels/10',
        data: { enabled: true },
      })
    )
  )
})

it('member removal requires confirmation and uses the scoped member endpoint', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace(['members.manage'])
  await userEvent.click(
    await screen.findByRole('button', { name: 'Remove from group' })
  )
  expect(request).not.toHaveBeenCalled()
  expect(
    screen.getByRole('heading', { name: 'Remove Member from alpha?' })
  ).toBeVisible()
  expect(
    screen.getByText(
      'The account returns to the default group. Its balance, credentials and usage history are preserved.'
    )
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Remove' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'DELETE',
        url: '/api/managed-groups/alpha/members/3',
      })
    )
  )
})

it('member managers see an empty approval inbox and a copyable group name', async () => {
  renderWorkspace(['members.manage'])
  expect(
    await screen.findByText('No pending membership requests')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Copy group name' })).toBeVisible()
  expect(screen.getByRole('heading', { name: 'alpha' })).toBeVisible()
})

it('approval actions target only the selected group and retain the applicant after a failure', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockRejectedValue(new Error('Offline'))
  renderWorkspace(['members.manage'], 10, [
    { id: 7, username: 'applicant', display_name: 'New member', status: 1 },
  ])
  const inbox = await screen.findByRole('region', {
    name: 'Pending membership requests',
  })
  expect(
    within(inbox)
      .getByRole('button', { name: 'Approve' })
      .compareDocumentPosition(
        screen.getByRole('button', { name: 'Remove from group' })
      ) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  await userEvent.click(within(inbox).getByRole('button', { name: 'Approve' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/api/managed-groups/alpha/requests/7',
        data: { approve: true },
      })
    )
  )
  await waitFor(() =>
    expect(within(inbox).getByRole('button', { name: 'Approve' })).toBeEnabled()
  )
  expect(within(inbox).getByText('New member')).toBeVisible()
  expect(
    screen.queryByRole('tab', { name: 'Group channels' })
  ).not.toBeInTheDocument()
})

it('a user requests membership without gaining a management grant', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace([])
  await screen.findByText('No managed groups')
  await userEvent.click(screen.getByRole('button', { name: 'Join a group' }))
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Request to join a group' }),
    'beta'
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Request membership' })
  )
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/api/managed-groups/join',
        data: { group: 'beta' },
      })
    )
  )
  expect(
    screen.queryByRole('button', { name: 'Group authorization' })
  ).not.toBeInTheDocument()
})

it('group grant changes require the existing step-up flow and cannot be automatically replayed', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'admin.user.update',
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        proof_token: 'test-single-use-proof',
        method: '2fa',
        scope: 'admin.user.update',
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  })
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <GroupAccessDialog
        group='alpha'
        open
        onOpenChange={() => undefined}
        catalog={[{ key: 'members.read', label: 'View group members' }]}
      />
    </QueryClientProvider>
  )
  await userEvent.type(screen.getByLabelText('User ID'), '2')
  await userEvent.click(
    screen.getByRole('button', { name: 'Load permissions' })
  )
  const permission = await screen.findByRole('button', {
    name: 'View group members',
  })
  expect(
    screen.getByRole('button', { name: 'Save permissions' })
  ).toBeDisabled()
  await userEvent.click(permission)
  await userEvent.click(
    screen.getByRole('button', { name: 'Save permissions' })
  )
  expect(request).not.toHaveBeenCalled()
  await userEvent.type(
    await screen.findByLabelText('Authenticator code or backup code'),
    '123456'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'PUT',
        url: '/api/group-admin/alpha/grants/2',
        data: { permissions: ['members.read'] },
        headers: { 'X-Security-Proof': 'test-single-use-proof' },
        singleUseAuthorization: true,
      })
    )
  )
})

it('channel models expand on demand and search narrows the channel list', async () => {
  renderWorkspace(['channels.read'])
  await screen.findByText('Shared channel')
  const search = screen.getByRole('textbox', {
    name: 'Search channels or models',
  })
  await userEvent.type(search, 'example-model')
  expect(screen.queryByText('Pending channel')).not.toBeInTheDocument()
  const models = screen.getByRole('button', { name: 'Show models' })
  expect(models).toHaveAttribute('aria-expanded', 'false')
  await userEvent.click(models)
  expect(models).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByText('example-model')).toBeVisible()
  await userEvent.clear(search)
  expect(screen.getByText('Pending channel')).toBeVisible()
})

it('the workspace tabs can be switched by keyboard without exposing mutation controls', async () => {
  renderWorkspace(['members.read', 'channels.read'])
  await screen.findByText('Member')
  const members = screen.getByRole('tab', { name: 'Group members' })
  members.focus()
  await userEvent.keyboard('{ArrowRight}{Enter}')
  await screen.findByText('Shared channel')
  expect(screen.getByRole('tab', { name: 'Group channels' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(
    screen.queryByRole('button', { name: 'Create dedicated group channel' })
  ).not.toBeInTheDocument()
})

it('root picks a shared channel in the dialog before making it available to the selected group', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace(['channels.read', 'channels.attach'], 100)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Shared channel access' })
  )
  await userEvent.click(
    await screen.findByRole('combobox', { name: 'Shared channel' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: /Additional channel/ })
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Allow group to attach' })
  )
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'PUT',
        url: '/api/group-admin/alpha/channels/12',
        data: { allowed: true },
      })
    )
  )
})

it('revoking shared access requires confirmation and leaves the action scoped to its group', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace(['channels.read'], 100)
  await userEvent.click(
    await screen.findByRole('button', {
      name: 'More actions for Shared channel',
    })
  )
  await userEvent.click(
    await screen.findByRole('menuitem', {
      name: 'Revoke shared channel access',
    })
  )
  expect(request).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'PUT',
        url: '/api/group-admin/alpha/channels/10',
        data: { allowed: false },
      })
    )
  )
})

it('group wallet managers allocate only their group pool and cannot fund it', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: null } })
  renderWorkspace(['wallet.allocate'])
  await screen.findByRole('heading', { name: 'Group balance' })
  expect(
    screen.queryByRole('button', { name: 'Fund group pool' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Enable group wallet' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Allocate' }))
  await userEvent.type(screen.getByLabelText('Amount'), '2')
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/api/managed-groups/alpha/wallet/allocations/3',
        data: expect.objectContaining({
          delta: 1000000,
          operation_id: expect.any(String),
        }),
      })
    )
  )
})

it('group wallet reclaim is bounded by the selected member remaining allocation', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: null } })
  renderWorkspace(['wallet.allocate'])
  await screen.findByRole('heading', { name: 'Group balance' })
  await userEvent.click(screen.getByRole('button', { name: 'Reclaim' }))
  await userEvent.type(screen.getByLabelText('Amount'), '6')
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  await userEvent.clear(screen.getByLabelText('Amount'))
  await userEvent.type(screen.getByLabelText('Amount'), '3')
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/managed-groups/alpha/wallet/allocations/3',
        data: expect.objectContaining({ delta: -1500000 }),
      })
    )
  )
})

it('independent managers can reduce local credits without a global admin endpoint', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace(['wallet.manage', 'wallet.allocate'], 10, [], 'independent')
  expect(await screen.findByText('Independent group')).toBeVisible()
  expect(
    await screen.findByRole('button', { name: 'Pause group wallet' })
  ).toBeEnabled()
  expect(
    screen.queryByRole('button', { name: 'Change group type' })
  ).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', { name: 'Reduce group credits' })
  )
  await userEvent.type(screen.getByLabelText('Amount'), '11')
  await userEvent.type(screen.getByLabelText('Reason'), 'Adjust local budget')
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  await userEvent.clear(screen.getByLabelText('Amount'))
  await userEvent.type(screen.getByLabelText('Amount'), '2')
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/api/managed-groups/alpha/wallet/funds',
        data: expect.objectContaining({
          delta: -1000000,
          reason: 'Adjust local budget',
        }),
      })
    )
  )
})

it('credit management permission does not mint prepaid funds', async () => {
  renderWorkspace(['wallet.manage'], 10)
  expect(await screen.findByText('Prepaid allocation group')).toBeVisible()
  await screen.findByRole('heading', { name: 'Group balance' })
  expect(
    screen.queryByRole('button', { name: 'Fund group pool' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Increase group credits' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Pause group wallet' })
  ).not.toBeInTheDocument()
})

it('root confirms the specific group before selecting independent credits', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderWorkspace(['wallet.read'], 100)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Change group type' })
  )
  expect(request).not.toHaveBeenCalled()
  expect(
    screen.getByRole('heading', { name: 'Use independent credits' })
  ).toBeVisible()
  await userEvent.click(
    screen.getByRole('button', { name: 'Change group type' })
  )
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'PUT',
        url: '/api/group-admin/alpha/wallet/type',
        data: { mode: 'independent' },
      })
    )
  )
})

it('group settings and usage are distinct panels with no stale content after switching', async () => {
  renderWorkspace(
    ['members.read', 'channels.read', 'wallet.read'],
    1,
    [],
    'independent'
  )
  await screen.findByText('Member')
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Group settings' }))
  expect(await screen.findByText('Models and group rates')).toBeVisible()
  expect(screen.queryByText('Member')).not.toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'Group settings' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await user.click(screen.getByRole('tab', { name: 'Group usage' }))
  expect(await screen.findByText('No group usage yet')).toBeVisible()
  expect(screen.queryByText('Models and group rates')).not.toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'Group settings' })).toHaveAttribute(
    'aria-selected',
    'false'
  )
})
