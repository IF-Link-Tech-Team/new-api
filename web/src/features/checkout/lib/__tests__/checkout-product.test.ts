import assert from 'node:assert/strict'

const bunTestModule = 'bun:test'
const { test } = (await import(bunTestModule)) as {
  test: typeof import('node:test').test
}

const { findTrustedCheckoutPlan, formatCheckoutAmount } =
  await import('../checkout-product')

const plan = (overrides: Record<string, unknown> = {}) =>
  ({
    product_id: 'pro',
    plan: {
      id: 2,
      title: 'Pro',
      subtitle: '',
      price_amount: 79,
      currency: 'CNY',
      ...overrides,
    },
  }) as never

test('matches the server product id and formats the server amount', () => {
  const selected = findTrustedCheckoutPlan('pro', [plan()])
  assert.ok(selected)
  assert.equal(selected?.plan.id, 2)
  assert.equal(formatCheckoutAmount(selected), '¥79.00')
})

test('rejects URL/catalog-only ids, non-CNY plans, and duplicate ids', () => {
  assert.equal(findTrustedCheckoutPlan('lite', [plan()]), null)
  assert.equal(
    findTrustedCheckoutPlan('pro', [plan({ currency: 'USD' })]),
    null
  )
  assert.equal(findTrustedCheckoutPlan('pro', [plan(), plan()]), null)
})
