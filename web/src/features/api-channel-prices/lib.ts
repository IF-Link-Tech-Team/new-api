import { z } from 'zod'

import type { ChannelPriceRow } from './api'

const amount = z
  .string()
  .trim()
  .refine(
    (value) =>
      value === '' || (Number.isFinite(Number(value)) && Number(value) >= 0),
    'Enter a finite, non-negative price'
  )

export const channelPricesSchema = z.object({
  reason: z.string().trim().min(1).max(2000),
  rows: z
    .array(
      z
        .object({
          enabled: z.boolean(),
          input: amount,
          cached_input: amount,
          cache_creation: amount,
          output: amount,
          position: z
            .string()
            .refine(
              (value) =>
                value === '' || (/^\d+$/.test(value) && Number(value) <= 10000),
              'Enter a non-negative route position'
            ),
        })
        .superRefine((row, ctx) => {
          if (!row.enabled) return
          for (const key of [
            'input',
            'cached_input',
            'cache_creation',
            'output',
          ] as const) {
            if (row[key] === '') {
              ctx.addIssue({
                code: 'custom',
                path: [key],
                message: 'Enter a finite, non-negative price',
              })
            }
          }
        })
    )
    .max(500),
})

export type ChannelPricesForm = z.infer<typeof channelPricesSchema>

export function priceFormDefaults(rows: ChannelPriceRow[]): ChannelPricesForm {
  return {
    reason: '',
    rows: rows.map((row) => {
      const prices = row.price
        ? (JSON.parse(row.price.selling_price) as Record<string, number>)
        : {}
      return {
        enabled: row.price?.enabled ?? false,
        input: prices.input == null ? '' : String(prices.input),
        cached_input:
          prices.cached_input == null ? '' : String(prices.cached_input),
        cache_creation:
          prices.cache_creation == null ? '' : String(prices.cache_creation),
        output: prices.output == null ? '' : String(prices.output),
        position: row.price?.position == null ? '' : String(row.price.position),
      }
    }),
  }
}
