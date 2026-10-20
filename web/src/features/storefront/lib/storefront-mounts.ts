/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { StorefrontMount, StorefrontMountKind } from '../types'

const MOUNT_KINDS: ReadonlySet<string> = new Set(['plans', 'packs', 'zones'])

/** Find the runtime storefront mount containers embedded in the home HTML. */
export function readStorefrontMounts(root: ParentNode): StorefrontMount[] {
  const mounts: StorefrontMount[] = []
  for (const element of root.querySelectorAll<HTMLElement>(
    '[data-storefront]'
  )) {
    const kind = element.dataset.storefront
    if (!kind || !MOUNT_KINDS.has(kind)) continue
    const zone = element.dataset.storefrontZone?.trim()
    mounts.push({
      element,
      kind: kind as StorefrontMountKind,
      zone: zone || undefined,
    })
  }
  return mounts
}
