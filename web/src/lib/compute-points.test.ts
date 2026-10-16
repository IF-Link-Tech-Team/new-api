/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  cnyToComputePoints,
  formatComputePoints,
  quotaToComputePoints,
} from './compute-points'

test('converts RMB display value to compute points', () => {
  assert.equal(cnyToComputePoints(1), 2_000)
  assert.equal(cnyToComputePoints(5), 10_000)
})

test('converts quota with the audited runtime scale without changing quota', () => {
  const scale = { quotaPerUnit: 500_000, cnyPerUnit: 1 }
  assert.equal(quotaToComputePoints(500_000, scale), 2_000)
  assert.equal(quotaToComputePoints(2_500_000, scale), 10_000)
})

test('keeps small usage visible and leaves the input unchanged', () => {
  const quota = 1
  const points = quotaToComputePoints(quota, {
    quotaPerUnit: 500_000,
    cnyPerUnit: 1,
  })

  assert.equal(quota, 1)
  assert.equal(points, 0.004)
  assert.equal(formatComputePoints(points), '0.004 算力点')
})
