/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { test } from 'vitest'

import {
  buildChatCompletionsEndpoint,
  normalizeOpenAIBaseUrl,
  selectClassroomModel,
  shouldShowModelSquareNavigation,
} from '../classroom-quick-start'

test('prefers Qwen3.8 Flash before the classroom DeepSeek fallbacks', () => {
  assert.equal(
    selectClassroomModel([
      'gpt-4o-mini',
      'deepseek-v4-1-flash-260910',
      'deepseek-v4-flash-ga-260731',
      'qwen3.8-flash',
    ]),
    'qwen3.8-flash'
  )
})

test('uses the primary classroom model while the user model list is empty', () => {
  assert.equal(selectClassroomModel([]), 'qwen3.8-flash')
})

test('normalizes configured routes to an OpenAI-compatible base URL', () => {
  assert.equal(
    normalizeOpenAIBaseUrl(
      'https://token.iflink.tech/v1/chat/completions/',
      'https://fallback.example'
    ),
    'https://token.iflink.tech/v1'
  )
  assert.equal(
    buildChatCompletionsEndpoint('https://token.iflink.tech/v1'),
    'https://token.iflink.tech/v1/chat/completions'
  )
})

test('keeps the model square hidden unless the recovery flag is explicit', () => {
  assert.equal(shouldShowModelSquareNavigation(undefined), false)
  assert.equal(shouldShowModelSquareNavigation('false'), false)
  assert.equal(shouldShowModelSquareNavigation(' TRUE '), true)
})
