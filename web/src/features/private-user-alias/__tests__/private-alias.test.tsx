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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { PrivateUserAliasButton, PrivateUserName } from '..'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

function renderAliases(alias = '') {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role: 100 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: alias ? [{ user_id: 3, alias }] : [] },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <PrivateUserName userID={3} name='A' />
      <PrivateUserAliasButton userID={3} name='A' group='Astro' />
    </QueryClientProvider>
  )
  return client
}

it('changing the viewer never displays the previous viewer’s alias', async () => {
  renderAliases('AA')
  expect(await screen.findByText('A (AA)')).toBeVisible()
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: [] } })
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'other-viewer', role: 100 })
  await waitFor(() =>
    expect(screen.queryByText('A (AA)')).not.toBeInTheDocument()
  )
  expect(screen.getByText('A')).toBeVisible()
})

it('saving a private alias updates the name without changing the public user profile', async () => {
  renderAliases()
  const save = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Set private alias for A' })
  )
  await userEvent.type(
    await screen.findByLabelText('Private alias for A'),
    'AA'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save alias' }))
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith('/api/user/self/aliases/3', {
      alias: 'AA',
      group: 'Astro',
    })
  )
  expect(await screen.findByText('A (AA)')).toBeVisible()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('clearing an alias saves an empty value and restores the original name', async () => {
  renderAliases('AA')
  const save = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  await screen.findByText('A (AA)')
  await userEvent.click(
    screen.getByRole('button', { name: 'Set private alias for A' })
  )
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear alias' })
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save alias' }))
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith('/api/user/self/aliases/3', {
      alias: '',
      group: 'Astro',
    })
  )
  expect(await screen.findByText('A')).toBeVisible()
})

it('a rejected save keeps the dialog and the previous alias', async () => {
  renderAliases('AA')
  vi.spyOn(api, 'put').mockRejectedValue(new Error('Offline'))
  await screen.findByText('A (AA)')
  await userEvent.click(
    screen.getByRole('button', { name: 'Set private alias for A' })
  )
  const input = await screen.findByLabelText('Private alias for A')
  await userEvent.clear(input)
  await userEvent.type(input, 'BB')
  await userEvent.click(screen.getByRole('button', { name: 'Save alias' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save alias' })).toBeEnabled()
  )
  expect(screen.getByRole('dialog')).toBeVisible()
  expect(screen.getByText('A (AA)')).toBeVisible()
})

it('markup inside an alias stays plain text', async () => {
  renderAliases('<img src=x>')
  expect(await screen.findByText('A (<img src=x>)')).toBeVisible()
  expect(document.querySelector('img')).toBeNull()
})

it('a delayed response from the old viewer cannot reveal their alias after switching accounts', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'first', role: 100 })
  let finishFirst: (value: unknown) => void = () => undefined
  const first = new Promise((resolve) => {
    finishFirst = resolve
  })
  const get = vi
    .spyOn(api, 'get')
    .mockImplementationOnce(() => first as ReturnType<typeof api.get>)
    .mockResolvedValue({ data: { success: true, data: [] } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <PrivateUserName userID={3} name='A' />
    </QueryClientProvider>
  )
  await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
  useAuthStore.getState().auth.setUser({ id: 2, username: 'second', role: 100 })
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  finishFirst({
    data: { success: true, data: [{ user_id: 3, alias: 'Secret AA' }] },
  })
  await waitFor(() =>
    expect(client.getQueryState(['private-user-aliases', 1])?.status).toBe(
      'success'
    )
  )
  expect(screen.getByText('A')).toBeVisible()
  expect(screen.queryByText(/Secret AA/)).not.toBeInTheDocument()
})
