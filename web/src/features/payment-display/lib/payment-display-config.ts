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

export interface PaymentDisplayConfig {
  alipayQrCodeUrl: string
}

export const defaultPaymentDisplayConfig: PaymentDisplayConfig = {
  alipayQrCodeUrl: '',
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function normalizePaymentDisplayConfig(
  value: unknown,
  baseUrl: string
): PaymentDisplayConfig {
  if (!isRecord(value) || typeof value.alipayQrCodeUrl !== 'string') {
    return defaultPaymentDisplayConfig
  }

  const candidate = value.alipayQrCodeUrl.trim()
  if (!candidate) {
    return defaultPaymentDisplayConfig
  }

  try {
    const base = new URL(baseUrl)
    const url = new URL(candidate, base)
    const isSecure = url.protocol === 'https:'
    const isSameOriginDevelopmentUrl =
      base.protocol === 'http:' &&
      url.protocol === 'http:' &&
      url.origin === base.origin

    if (
      (!isSecure && !isSameOriginDevelopmentUrl) ||
      url.username ||
      url.password
    ) {
      return defaultPaymentDisplayConfig
    }

    return { alipayQrCodeUrl: url.href }
  } catch {
    return defaultPaymentDisplayConfig
  }
}

export async function loadPaymentDisplayConfig(
  fetcher: typeof fetch = fetch,
  baseUrl: string = window.location.origin
): Promise<PaymentDisplayConfig> {
  try {
    const response = await fetcher('/payment-display.json', {
      cache: 'no-store',
      credentials: 'same-origin',
    })
    if (!response.ok) {
      return defaultPaymentDisplayConfig
    }

    return normalizePaymentDisplayConfig(await response.json(), baseUrl)
  } catch {
    return defaultPaymentDisplayConfig
  }
}
