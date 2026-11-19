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
import type {
  BillingPluginVariant,
  BillingUsageSchema,
  PricingModel,
} from '../types'
import { compileBillingExpression } from './billing-expression/parser'
import { TIME_FUNCTIONS, visitExpression } from './billing-expression/types'

export function splitPluginBillingExprKey(
  key: string
): [string, string] | null {
  const separator = key.indexOf('::')
  if (separator < 1) return null
  const plugin = key.slice(0, separator)
  const model = key.slice(separator + 2)
  if (!/^[a-z0-9][a-z0-9_-]{0,29}$/.test(plugin) || !model.trim()) return null
  return [plugin, model]
}

// Undefined means the browser parser cannot check this expression; the server
// remains authoritative for expression validation and pricing compatibility.
export function taskExpressionCompatible(
  expression: string,
  schema: BillingUsageSchema
): boolean | undefined {
  if (!expression.trim()) return false
  const compiled = compileBillingExpression(expression)
  if (compiled.status !== 'ready') return undefined
  let compatible = !compiled.functions.has('fixed')
  visitExpression(compiled.ast, (node) => {
    if (node.kind !== 'call' || node.name !== 'u') return
    const key = node.args[0]
    if (
      key?.kind === 'literal' &&
      typeof key.value === 'string' &&
      !Object.hasOwn(schema, key.value)
    ) {
      compatible = false
    }
  })
  return compatible
}

export function withPluginPricing(
  model: PricingModel,
  variant: BillingPluginVariant
): PricingModel {
  return {
    ...model,
    quota_type: variant.billing_mode === 'ratio' ? model.quota_type : 0,
    billing_mode: variant.billing_mode ?? 'tiered_expr',
    billing_expr: variant.billing_expr,
    billing_usage_schema: Object.keys(variant.billing_usage_schema ?? {}).length
      ? variant.billing_usage_schema
      : undefined,
    billing_usage_examples: variant.billing_usage_examples,
    billing_plugin_variants: undefined,
    group_pricing: undefined,
    enable_groups: variant.group ? [variant.group] : model.enable_groups,
  }
}

/** Select only server-authorized group prices; legacy catalogs are unchanged. */
export function withGroupPricing(
  model: PricingModel,
  group?: string
): PricingModel {
  if (!model.group_pricing?.length) return model
  const variants = model.group_pricing.filter(
    (variant) =>
      model.enable_groups.includes(variant.group) &&
      (!group || group === 'all' || variant.group === group)
  )
  if (variants.length === 1) {
    return {
      ...withPluginPricing(model, variants[0]),
      group_pricing: model.group_pricing,
    }
  }
  const first = variants[0]
  return {
    ...model,
    billing_mode: 'tiered_expr',
    // Keep the minute clock active if any visible price has time-based tiers.
    billing_expr:
      variants.find((variant) => {
        const compiled = compileBillingExpression(variant.billing_expr)
        return (
          compiled.status === 'ready' &&
          TIME_FUNCTIONS.some((name) => compiled.functions.has(name))
        )
      })?.billing_expr ?? first?.billing_expr,
    billing_usage_schema: undefined,
    billing_usage_examples: undefined,
    billing_plugin_variants: variants,
  }
}

export function pluginExpressionsEqual(
  left: Record<string, string> = {},
  right: Record<string, string> = {}
): boolean {
  const keys = Object.keys(left)
  return (
    keys.length === Object.keys(right).length &&
    keys.every((key) => Object.hasOwn(right, key) && left[key] === right[key])
  )
}

export function pluginUsageSchema(
  model: PricingModel | undefined,
  pluginKey: string | undefined
): BillingUsageSchema | undefined {
  const variant = model?.billing_plugin_variants?.find(
    (item) => item.plugin_key === pluginKey
  )
  return variant?.billing_usage_schema ?? model?.billing_usage_schema
}
