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

import { subscriptionPlanSchema } from '../../types'
import { ProductPublicationDialog } from '../product-publication-dialog'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const plan = subscriptionPlanSchema.parse({
  id: 7,
  title: 'Offer',
  price_amount: 10,
  currency: 'USD',
  duration_unit: 'month',
  duration_value: 1,
  quota_reset_period: 'never',
  enabled: true,
  sort_order: 0,
  max_purchase_per_user: 0,
  total_amount: 1000,
  model_scope: 'shared-model',
  price_book: 'product:7',
  price_version: 'v1',
  config_revision: 4,
})
const snapshot = {
  id: 31,
  user_id: 41,
  amount_total: 1000,
  amount_used: 300,
  end_time: 1793500000,
  model_scope: 'shared-model',
  price_version: 'v1',
  reset_period: 'never',
  reset_seconds: 0,
}

function dialog() {
  const close = vi.fn(),
    published = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ProductPublicationDialog
        planID={7}
        beforePlan={plan}
        payload={{ plan: { ...plan, total_amount: 800 }, expected_revision: 4 }}
        onClose={close}
        onPublished={published}
      />
    </QueryClientProvider>
  )
  return { close, published }
}

it('previews changed quota by default and publishes only after showing affected users and preserving usage', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: String(url).endsWith('publication-preview')
        ? {
            hash: 'a'.repeat(64),
            affected: 1,
            blocked: 0,
            changes: [
              { before: snapshot, after: { ...snapshot, amount_total: 800 } },
            ],
          }
        : { id: 1, revision: 5 },
    },
  }))
  const callbacks = dialog()
  expect(
    screen.getByRole('checkbox', { name: 'Total entitlement quota' })
  ).toBeChecked()
  expect(
    screen.getByRole('checkbox', { name: 'Quota reset schedule' })
  ).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Preview impact' })).toBeDisabled()
  await user.type(screen.getByLabelText('Reason'), 'Reviewed adjustment')
  await user.click(screen.getByRole('button', { name: 'Preview impact' }))
  expect(await screen.findByText('#41')).toBeInTheDocument()
  expect(screen.getByRole('row', { name: /#41/ })).toHaveTextContent('300')
  expect(callbacks.published).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: 'Publish and synchronize' })
  )
  await waitFor(() => expect(callbacks.published).toHaveBeenCalledOnce())
  expect(post.mock.calls[1][1]).toMatchObject({
    fields: ['quota'],
    preview_hash: 'a'.repeat(64),
    expected_revision: 4,
  })
  expect(post.mock.calls[1][1]).toHaveProperty(
    'request_key',
    (post.mock.calls[0][1] as { request_key: string }).request_key
  )
})

it('blocks an invalid decrease and discards the preview when sync fields change', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        hash: 'b'.repeat(64),
        affected: 1,
        blocked: 1,
        changes: [
          {
            before: snapshot,
            after: snapshot,
            blocker: 'new quota is below used or reserved quota',
          },
        ],
      },
    },
  })
  dialog()
  await user.type(screen.getByLabelText('Reason'), 'Adjustment')
  await user.click(screen.getByRole('button', { name: 'Preview impact' }))
  expect(
    await screen.findByRole('button', { name: 'Publish and synchronize' })
  ).toBeDisabled()
  await user.click(
    screen.getByRole('checkbox', { name: 'Total entitlement quota' })
  )
  expect(screen.getByRole('button', { name: 'Preview impact' })).toBeEnabled()
  expect(post).toHaveBeenCalledTimes(1)
})

it('keeps the editor on a revision conflict and requires a fresh preview', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { hash: 'c'.repeat(64), affected: 0, blocked: 0, changes: [] },
      },
    })
    .mockRejectedValueOnce(
      new Error('product publication preview changed; preview again')
    )
  const callbacks = dialog()
  await user.type(screen.getByLabelText('Reason'), 'Adjustment')
  await user.click(screen.getByRole('button', { name: 'Preview impact' }))
  await user.click(
    await screen.findByRole('button', { name: 'Publish and synchronize' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Preview impact' })).toBeVisible()
  )
  expect(callbacks.published).not.toHaveBeenCalled()
  expect(callbacks.close).not.toHaveBeenCalled()
})
