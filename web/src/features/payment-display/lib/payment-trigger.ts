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

export interface PaymentDisplaySelection {
  productId: string
}

interface AttributeReader {
  getAttribute: (name: string) => string | null
}

function isAttributeReader(value: unknown): value is AttributeReader {
  return (
    typeof value === 'object' &&
    value !== null &&
    'getAttribute' in value &&
    typeof value.getAttribute === 'function'
  )
}

export function paymentSelectionFromEventPath(
  path: readonly EventTarget[]
): PaymentDisplaySelection | null {
  for (const target of path) {
    if (!isAttributeReader(target)) {
      continue
    }

    const productId = target.getAttribute('data-payment-product-id')?.trim()
    if (productId) {
      return { productId }
    }
  }

  return null
}

export function consumePaymentSelection(
  event: MouseEvent
): PaymentDisplaySelection | null {
  // A portal in the shadow root and its host can both dispatch React handlers
  // for the same native click. Claim it before starting checkout or login.
  if (event.defaultPrevented) return null

  let path = event.target ? [event.target] : []
  if (typeof event.composedPath === 'function') path = event.composedPath()
  const selection = paymentSelectionFromEventPath(path)
  if (selection) event.preventDefault()
  return selection
}
