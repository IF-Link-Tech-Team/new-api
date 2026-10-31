/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

export const CLASSROOM_PREFERRED_MODELS = [
  'qwen3.8-flash',
  'deepseek-v4.1-flash-1017',
] as const

export function selectClassroomModel(models: readonly string[]): string {
  return (
    CLASSROOM_PREFERRED_MODELS.find((model) => models.includes(model)) ??
    models[0] ??
    CLASSROOM_PREFERRED_MODELS[0]
  )
}

export function normalizeOpenAIBaseUrl(
  sourceUrl: string | undefined,
  currentOrigin: string
): string {
  const trimmed = sourceUrl?.trim()
  if (!trimmed) return `${currentOrigin.replace(/\/+$/, '')}/v1`

  const withoutTrailingSlash = trimmed.replace(/\/+$/, '')
  if (withoutTrailingSlash.endsWith('/v1/chat/completions')) {
    return withoutTrailingSlash.slice(0, -'/chat/completions'.length)
  }
  if (withoutTrailingSlash.endsWith('/v1')) return withoutTrailingSlash
  return `${withoutTrailingSlash}/v1`
}

export function buildChatCompletionsEndpoint(baseUrl: string): string {
  return `${baseUrl.replace(/\/+$/, '')}/chat/completions`
}

export function shouldShowModelSquareNavigation(
  featureFlag: string | boolean | undefined
): boolean {
  if (typeof featureFlag === 'boolean') return featureFlag
  return featureFlag?.trim().toLowerCase() === 'true'
}
