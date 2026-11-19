import { render, screen, cleanup } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { ModelDetailsApi } from '../components/model-details-api'
import { getTaskPricingDisplayTiers } from '../lib/task-matrix-display'
import type { PricingModel, BillingUsageSchema } from '../types'

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: { server_address: 'https://token.example' } }),
}))
vi.mock('@/components/ai-elements/code-block', () => ({
  CodeBlock: (props: { code: string }) => (
    <pre data-testid='sample'>{props.code}</pre>
  ),
  CodeBlockCopyButton: () => null,
}))
afterEach(cleanup)
const model: PricingModel = {
  id: 1,
  model_name: 'minimax-video-h3',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
  supported_endpoint_types: ['minimax-video'],
}
it('shows MiniMax video input instead of a chat request', () => {
  render(
    <ModelDetailsApi
      model={model}
      endpointMap={{ 'minimax-video': { path: '/v2/video_generation' } }}
    />
  )
  const sample = screen.getByTestId('sample').textContent || ''
  expect(sample).toContain('/v2/video_generation')
  expect(sample).toContain('"content"')
  expect(sample).not.toContain('messages')
  expect(sample).not.toContain('\\n')
  expect(screen.queryByText('temperature')).not.toBeInTheDocument()
})
it('shows reference audio input for IndexTTS', () => {
  render(
    <ModelDetailsApi
      model={{
        ...model,
        model_name: 'indextts-2',
        supported_endpoint_types: ['tokenhub-audio'],
      }}
      endpointMap={{
        'tokenhub-audio': { path: '/tokenhub-media/v1/audio/generations' },
      }}
    />
  )
  const sample = screen.getByTestId('sample').textContent || ''
  expect(sample).toContain('/tokenhub-media/v1/audio/generations')
  expect(sample).toContain('"voice"')
  expect(sample).not.toContain('messages')
})

it('shows HY3D submit and query examples without chat parameters', () => {
  render(
    <ModelDetailsApi
      model={{
        ...model,
        model_name: 'hy-3d-3.1',
        supported_endpoint_types: ['tokenhub-3d'],
      }}
      endpointMap={{
        'tokenhub-3d': { path: '/tokenhub-media/v1/3d/generations' },
      }}
    />
  )
  const samples = screen
    .getAllByTestId('sample')
    .map((node) => node.textContent)
    .join('\n')
  expect(samples).toContain('/tokenhub-media/v1/3d/generations')
  expect(samples).toContain('/tokenhub-media/v1/tasks/<TASK_ID>')
  expect(samples).toContain('"generate_type": "Normal"')
  expect(samples).not.toContain('messages')
  expect(samples).not.toContain('\\n')
})

it('expands HY3D base and extra fees without charging PBR for geometry', () => {
  const schema: BillingUsageSchema = {
    model_count: { type: 'number' as const, unit: 'count' },
    generate_type: { enum: ['Normal', 'Geometry'] },
    pbr: { type: 'number' as const, unit: 'count' },
    custom_face_count: { type: 'number' as const, unit: 'count' },
    multi_view: { type: 'number' as const, unit: 'count' },
    extra_format: { type: 'number' as const, unit: 'count' },
  }
  const tiers = getTaskPricingDisplayTiers(
    'u("generate_type") == "Geometry" ? tier("geometry", u("model_count") * 1.8 + u("custom_face_count") * 1.2 + u("multi_view") * 1.2 + u("extra_format") * 0.6) : tier("textured", u("model_count") * 2.4 + u("pbr") * 1.2 + u("custom_face_count") * 1.2 + u("multi_view") * 1.2 + u("extra_format") * 0.6)',
    schema
  )
  expect(tiers).toHaveLength(2)
  const geometry = tiers.find((tier) =>
    tier.conditions.some((condition) => condition.value === 'Geometry')
  )
  const normal = tiers.find((tier) =>
    tier.conditions.some((condition) => condition.value === 'Normal')
  )
  expect(geometry?.unitPrices).toMatchObject({
    model_count: 1.8,
    custom_face_count: 1.2,
    multi_view: 1.2,
    extra_format: 0.6,
  })
  expect(geometry?.unitPrices.pbr ?? 0).toBe(0)
  expect(normal?.unitPrices).toMatchObject({
    model_count: 2.4,
    pbr: 1.2,
    custom_face_count: 1.2,
    multi_view: 1.2,
    extra_format: 0.6,
  })
})
