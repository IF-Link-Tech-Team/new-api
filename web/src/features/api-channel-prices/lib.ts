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
          expression_mode: z.boolean(),
          billing_expression: z.string().max(20000),
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
          if (row.expression_mode) {
            if (!row.billing_expression.trim()) {
              ctx.addIssue({
                code: 'custom',
                path: ['billing_expression'],
                message: 'Enter a billing expression',
              })
            }
            return
          }
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
      const prices = row.price?.selling_price
        ? (JSON.parse(row.price.selling_price) as Record<string, number>)
        : {}
      return {
        enabled: row.price?.enabled ?? false,
        expression_mode: !!row.price?.billing_expression?.trim(),
        billing_expression: row.price?.billing_expression ?? '',
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
