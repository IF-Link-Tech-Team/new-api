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

import { GroupModelSettings } from '../group-model-settings'
import { GroupUsagePanel } from '../group-usage-panel'

const group = {
  name: 'Astro',
  mode: 'independent' as const,
  permissions: ['channels.read', 'channels.manage', 'wallet.manage'],
}
function renderSettings(permissions = group.permissions) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: String(url).endsWith('/channels')
        ? [
            {
              id: 22,
              name: 'Astro',
              owned: true,
              models: 'deepseek-v4.1-flash-1017',
              status: 1,
              reviewed: true,
            },
          ]
        : [
            {
              channel_id: 22,
              channel_name: 'Astro',
              model: 'deepseek-v4.1-flash-1017',
              revision: 0,
              prices: null,
              inherited_prices: {
                input: 0,
                cached_input: 0,
                cache_creation: 0,
                output: 0,
              },
            },
          ],
    },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <GroupModelSettings group={{ ...group, permissions }} />
    </QueryClientProvider>
  )
}
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})
it('requires four explicit group rates and saves only the selected group', async () => {
  const request = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true } })
  renderSettings()
  const user = userEvent.setup()
  expect(await screen.findByText('Using existing channel price')).toBeVisible()
  await user.click(screen.getByRole('button', { name: /Set rate for/ }))
  const dialog = screen.getByRole('dialog')
  for (const label of ['Input', 'Cache read', 'Cache creation', 'Output']) {
    expect(within(dialog).getByRole('spinbutton', { name: label })).toHaveValue(
      null
    )
  }
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))
  expect(request).not.toHaveBeenCalled()
  for (const [label, value] of [
    ['Input', '2'],
    ['Cache read', '0.2'],
    ['Cache creation', '0'],
    ['Output', '8'],
  ]) {
    await user.type(
      within(dialog).getByRole('spinbutton', { name: label }),
      value
    )
  }
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'PUT',
        url: '/api/managed-groups/Astro/channels/22/prices',
        data: {
          model: 'deepseek-v4.1-flash-1017',
          expected_revision: 0,
          prices: { input: 2, cached_input: 0.2, cache_creation: 0, output: 8 },
        },
      })
    )
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})
it('read-only group access shows settings without write controls', async () => {
  renderSettings(['channels.read'])
  expect(await screen.findByText('Models and group rates')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Manage models' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /Set rate/ })
  ).not.toBeInTheDocument()
})
it('model changes submit the expected old list and retain the form after a server conflict', async () => {
  const request = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: false,
      message: 'Group channel models changed; reload before saving',
    },
  })
  renderSettings()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Manage models' }))
  const dialog = screen.getByRole('dialog')
  const input = within(dialog).getByRole('textbox', {
    name: 'Models (comma-separated)',
  })
  await user.type(input, ',new-model')
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/managed-groups/Astro/channels/22/models',
        data: {
          models: 'deepseek-v4.1-flash-1017,new-model',
          expected_models: 'deepseek-v4.1-flash-1017',
        },
      })
    )
  )
  expect(dialog).toBeVisible()
  expect(input).toHaveValue('deepseek-v4.1-flash-1017,new-model')
})
it('usage includes zero-priced calls and requests only the selected group', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 1,
            request_id: 'test',
            created_at: 1700000000,
            user_id: 3,
            username: 'Student',
            model_name: 'deepseek-v4.1-flash-1017',
            quota: 0,
            prompt_tokens: 100,
            completion_tokens: 20,
          },
        ],
        has_more: false,
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <GroupUsagePanel group='Astro' />
    </QueryClientProvider>
  )
  expect(await screen.findByText('Free call')).toBeVisible()
  expect(screen.getByText('Student')).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/managed-groups/Astro/usage?page=1')
  expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
})
