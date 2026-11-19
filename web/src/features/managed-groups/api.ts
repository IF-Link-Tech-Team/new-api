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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface ManagedGroup {
  name: string
  mode?: 'prepaid' | 'independent'
  permissions: string[]
}
export interface GroupOverview {
  groups: ManagedGroup[]
  permissions: { key: string; label: string }[]
  join_request: { group_name: string }
}
export interface GroupMember {
  id: number
  username: string
  display_name: string
  status: number
}
export interface GroupMembers {
  items: GroupMember[]
  total: number
  requests: GroupMember[]
}
export interface GroupChannel {
  id: number
  name: string
  models: string
  status: number
  owned: boolean
  reviewed: boolean
  available: boolean
  attached: boolean
  can_detach: boolean
}
export interface ChannelCatalogItem {
  id: number
  name: string
  group: string
}
export const groupPath = (group: string) =>
  `/api/managed-groups/${encodeURIComponent(group)}`
export const adminGroupPath = (group: string) =>
  `/api/group-admin/${encodeURIComponent(group)}`

export async function groupGet<T>(url: string): Promise<T> {
  const response = await api.get(url)
  return requireServerSuccess(response.data).data as T
}

export async function groupChange(input: {
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  url: string
  data?: unknown
  proof?: string
}): Promise<void> {
  const response = await api.request({
    method: input.method,
    url: input.url,
    data: input.data,
    headers: input.proof ? { 'X-Security-Proof': input.proof } : undefined,
    singleUseAuthorization: !!input.proof,
  })
  requireServerSuccess(response.data)
}
