/*
Copyright (C) 2026 new-api-sub contributors

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
*/
import type { ChannelExtra } from '@/features/channels/types'
import { api } from '@/lib/api'

export type ChannelTemplate = {
  id: number
  name: string
  channel_type: number
  base_url: string
  allow_custom_base_url: boolean
  models: string
  group: string
  priority: number
  weight: number
  test_model: string
  extra: ChannelExtra | null
  enabled: boolean
  /** Pools its new channels are pushed to, in order. */
  pool_ids: number[]
  created_at: number
  updated_at: number
}

export type ChannelTemplateInput = Omit<
  ChannelTemplate,
  'id' | 'created_at' | 'updated_at'
>

export const templatesQueryKey = ['admin-templates'] as const

export async function getTemplates(): Promise<ChannelTemplate[]> {
  return (await api.get<ChannelTemplate[]>('/api/templates')).data
}

export async function createTemplate(body: ChannelTemplateInput) {
  await api.post('/api/templates', body)
}

export async function updateTemplate(id: number, body: ChannelTemplateInput) {
  await api.put(`/api/templates/${id}`, body)
}

export async function deleteTemplate(id: number) {
  await api.delete(`/api/templates/${id}`)
}
