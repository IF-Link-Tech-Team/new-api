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
import dayjs from 'dayjs'

import { parsePriceComponents } from './price-form'

// "1 / 0.02 / 4" style summary of a price-components JSON string
// (input / cached input / output), or the raw value when unparsable.
export function formatPriceComponents(raw: string): string {
  const components = parsePriceComponents(raw)
  if (!components) return raw || '—'
  return `${components.input} / ${components.cached_input} / ${components.output}`
}

export function formatEffectiveAt(timestamp: number): string {
  if (!timestamp) return '—'
  return dayjs.unix(timestamp).format('YYYY-MM-DD HH:mm')
}
