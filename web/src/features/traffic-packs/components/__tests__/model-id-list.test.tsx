/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { ModelIdList } from '../model-id-list'

test('copies the exact model ID while showing each configured multiplier', async () => {
  const user = userEvent.setup()
  const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  render(
    <ModelIdList
      models={['qwen3.8-flash', 'deepseek-v4-1-flash-260910']}
      billingMultipliers={{
        'qwen3.8-flash': 1,
        'deepseek-v4-1-flash-260910': 0.6,
      }}
    />
  )
  expect(screen.getByText('官网价 ×1.00')).toBeVisible()
  expect(screen.getByText('官网价 ×0.60')).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: '复制模型 ID qwen3.8-flash' })
  )
  await waitFor(() => expect(copy).toHaveBeenCalledWith('qwen3.8-flash'))
  expect(screen.getByRole('button', { name: 'Copied' })).toBeVisible()
})

test('retains a retryable copy button when clipboard methods fail', async () => {
  const user = userEvent.setup()
  vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(
    new Error('Clipboard unavailable')
  )
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: () => false,
  })
  render(<ModelIdList models={['qwen3.8-flash']} />)
  const button = screen.getByRole('button', {
    name: '复制模型 ID qwen3.8-flash',
  })
  await user.click(button)
  await waitFor(() => expect(button).toBeEnabled())
  expect(button).toHaveAccessibleName('复制模型 ID qwen3.8-flash')
})
