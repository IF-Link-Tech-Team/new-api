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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { describe, expect, test } from 'vitest'

import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../../lib/channel-form'
import {
  isValidUpstreamCosts,
  parseUpstreamCosts,
} from '../../lib/upstream-costs'
import { channelSchema } from '../../types'
import { ChannelUpstreamCosts } from '../channel-upstream-costs'

function CostsForm(props: {
  disabled?: boolean
  onSave: (value: string) => void
}) {
  const form = useForm<ChannelFormValues>({
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      models: 'deepseek',
      upstream_costs: '{}',
    },
  })
  return (
    <QueryClientProvider client={new QueryClient()}>
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit((values) =>
            props.onSave(values.upstream_costs || '{}')
          )}
        >
          <ChannelUpstreamCosts disabled={props.disabled} />
          <button type='submit'>Save</button>
        </form>
      </Form>
    </QueryClientProvider>
  )
}
describe('channel procurement cost editing', () => {
  test('adds four costs and preserves them through the API form round trip', async () => {
    let saved = ''
    render(
      <CostsForm
        onSave={(value) => {
          saved = value
        }}
      />
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Add procurement costs' })
    )
    const inputs = screen.getAllByRole('spinbutton')
    for (const [i, value] of ['1', '0.1', '0', '4'].entries()) {
      await user.type(inputs[i], value)
    }
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(JSON.parse(saved)).toEqual({
      deepseek: {
        input: 1,
        cached_input: 0.1,
        cache_creation: 0,
        output: 4,
        currency: 'CNY',
      },
    })
    const channel = channelSchema.parse({
      id: 2,
      name: 'channel',
      type: 1,
      key: '',
      models: 'deepseek',
      group: 'default',
      status: 1,
      channel_info: {},
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      upstream_costs: saved,
    })
    const values = transformChannelToFormDefaults(channel)
    expect(transformFormDataToUpdatePayload(values, 2).upstream_costs).toBe(
      saved
    )
    expect(channelFormSchema.safeParse(values).success).toBe(true)
  })
  test('unknown values cannot be treated as free and legacy cache omissions remain compatible', () => {
    expect(isValidUpstreamCosts('{"deepseek":{"input":null,"output":4}}')).toBe(
      false
    )
    expect(
      isValidUpstreamCosts(
        '{"deepseek":{"input":1,"cached_input":-1,"output":4}}'
      )
    ).toBe(false)
    expect(isValidUpstreamCosts('[]')).toBe(false)
    expect(parseUpstreamCosts('{"deepseek":{"input":1,"output":4}}')).toEqual({
      deepseek: { input: 1, output: 4, cached_input: 0, cache_creation: 0 },
    })
  })
  test('saves DeepSeek peak windows and explicit public holiday overrides', async () => {
    let saved = ''
    render(
      <CostsForm
        onSave={(value) => {
          saved = value
        }}
      />
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Add procurement costs' })
    )
    await user.click(
      screen.getByRole('button', { name: 'Configure DeepSeek peak/off-peak' })
    )
    for (const [i, input] of screen.getAllByRole('spinbutton').entries()) {
      await user.type(input, String(i + 1))
    }
    await user.type(
      screen.getByLabelText(
        'deepseek Public holidays / off-peak dates (YYYY-MM-DD, comma separated)'
      ),
      '2026-10-01'
    )
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(isValidUpstreamCosts(saved)).toBe(true)
    expect(JSON.parse(saved).deepseek.peak).toEqual({
      prices: { input: 5, cached_input: 6, cache_creation: 7, output: 8 },
      weekdays: [1, 2, 3, 4, 5],
      windows: ['09:00-12:00', '14:00-18:00'],
      offpeak_dates: ['2026-10-01'],
    })
  })
  test('restricted administrators cannot edit cost fields', () => {
    render(<CostsForm disabled onSave={() => {}} />)
    expect(
      screen.getByRole('button', { name: 'Add procurement costs' })
    ).toBeDisabled()
  })
})
