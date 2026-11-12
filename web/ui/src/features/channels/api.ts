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
// Modified 2026-11-06 by new-api-sub: only the calls our backend serves: channel rows, types, owner options, the keys of a multi-key channel and the /api/newapi proxies
import { api } from '@/lib/api'

import type {
  Channel,
  ChannelKeys,
  ChannelKeyTestResult,
  ChannelOwnerOption,
  ChannelTestResponse,
  ChannelTypeDefaults,
  ChannelView,
  CreateChannelPayload,
} from './types'

/** The whole scope at once; the table filters, sorts and pages on the client. */
export async function getChannels(view?: ChannelView): Promise<Channel[]> {
  const res = await api.get<Channel[]>('/api/channels', { params: { view } })
  return res.data
}

export async function getChannelTypes(): Promise<ChannelTypeDefaults[]> {
  const res = await api.get<ChannelTypeDefaults[]>('/api/channel-types')
  return res.data
}

export async function createChannel(
  payload: CreateChannelPayload
): Promise<{ ids: number[] }> {
  const res = await api.post<{ ids: number[] }>('/api/channels', payload)
  return res.data
}

/**
 * The server sends new-api these two fields and nothing else. Like the status
 * change, a 202 means some pools catch up in the background: still a success.
 */
export async function updateChannel(
  id: number,
  payload: { name: string; models: string }
): Promise<void> {
  await api.put(`/api/channels/${id}`, payload)
}

export async function testChannel(
  id: number,
  payload: { model?: string; endpoint_type?: string; stream?: boolean }
): Promise<ChannelTestResponse> {
  const res = await api.post<ChannelTestResponse>(
    `/api/channels/${id}/test`,
    payload
  )
  return res.data
}

export async function setChannelStatus(
  id: number,
  status: number
): Promise<void> {
  await api.post(`/api/channels/${id}/status`, { status })
}

export async function updateChannelBalance(
  id: number
): Promise<{ balance?: number; raw_response?: string }> {
  const res = await api.post(`/api/channels/${id}/balance`)
  return res.data
}

export async function getChannelKeys(id: number): Promise<ChannelKeys> {
  const res = await api.get<ChannelKeys>(`/api/channels/${id}/keys`)
  return res.data
}

/** Stops at the first failure: done lists what changed, error says why it stopped. */
export async function setChannelKeysEnabled(
  id: number,
  indexes: number[],
  enabled: boolean
): Promise<{ done: number[]; error: string }> {
  const res = await api.post<{ done: number[] | null; error: string }>(
    `/api/channels/${id}/keys/${enabled ? 'enable' : 'disable'}`,
    { indexes }
  )
  return { done: res.data.done ?? [], error: res.data.error }
}

/** Keys the server had no time left to start are missing from the results. */
export async function testChannelKeys(
  id: number,
  indexes: number[],
  model: string
): Promise<ChannelKeyTestResult[]> {
  const res = await api.post<{ results: ChannelKeyTestResult[] | null }>(
    `/api/channels/${id}/keys/test`,
    { indexes, model }
  )
  return res.data.results ?? []
}

/** Without indexes every key is queried; returns the refreshed list. */
export async function queryChannelKeyBalances(
  id: number,
  indexes?: number[]
): Promise<ChannelKeys> {
  const res = await api.post<ChannelKeys>(
    `/api/channels/${id}/keys/balance`,
    indexes && { indexes }
  )
  return res.data
}

/** Soft delete: the channel is hidden and disabled, and an admin can restore it. */
export async function deleteChannel(id: number): Promise<void> {
  await api.delete(`/api/channels/${id}`)
}

export async function restoreChannel(id: number): Promise<void> {
  await api.post(`/api/channels/${id}/restore`)
}

/** Only for orphans: drops the local record of a channel new-api no longer has. */
export async function purgeChannel(id: number): Promise<void> {
  await api.delete(`/api/channels/${id}`, { params: { purge: 1 } })
}

/** Admins: a failed push to one pool goes back in the queue. */
export async function retryChannelPlacement(
  id: number,
  poolId: number
): Promise<void> {
  await api.post(`/api/channels/${id}/placements/${poolId}/retry`)
}

/** Super only: drops a push to one pool that has not created a channel; 204. */
export async function cancelChannelPlacement(
  id: number,
  poolId: number
): Promise<void> {
  await api.delete(`/api/channels/${id}/placements/${poolId}`)
}

/** Admin app only: portal users in the caller's scope. */
export async function getChannelOwnerOptions(): Promise<ChannelOwnerOption[]> {
  const res = await api.get<ChannelOwnerOption[]>('/api/users')
  return res.data
}

// ============================================================================
// new-api data for the template form; the proxy answers supers only
// ============================================================================

export type TaskPluginOption = {
  sortPriority?: number
  website?: string
  key: string
  name: string
  description?: Record<string, string> | null
  icon?: string
  hasIcon?: boolean
  baseUrl?: string
  models: string[]
  channelTypes?: number[] | null
  upstreams?: string[] | null
}

export async function getChannelDefaultBaseURLs(): Promise<
  Partial<Record<number, string>>
> {
  const res = await api.get<Partial<Record<number, string>>>(
    '/api/newapi/default-base-urls'
  )
  return res.data
}

export async function getGroups(): Promise<string[]> {
  const res = await api.get<string[] | null>('/api/newapi/groups')
  return res.data ?? []
}

export async function getAllModels(): Promise<
  Array<{ id: string; [key: string]: unknown }>
> {
  const res = await api.get<Array<{
    id: string
    [key: string]: unknown
  }> | null>('/api/newapi/models')
  return res.data ?? []
}

export async function getPrefillGroups(
  type: 'model' | 'group' = 'model'
): Promise<Array<{ id: number; name: string; items: string | string[] }>> {
  const res = await api.get<Array<{
    id: number
    name: string
    items: string | string[]
  }> | null>('/api/newapi/prefill-groups', { params: { type } })
  return res.data ?? []
}

export type PoolOption = { id: number; name: string; accept_new: boolean }

/** The pools a template can push to. */
export async function getPoolOptions(): Promise<PoolOption[]> {
  const res = await api.get<PoolOption[] | null>('/api/pools')
  return (res.data ?? []).map(({ id, name, accept_new }) => ({
    id,
    name,
    accept_new,
  }))
}

/** Lists an upstream's models with the connection typed into the form. */
export async function fetchChannelModels(payload: {
  type: number
  base_url: string
  key: string
}): Promise<string[]> {
  const res = await api.post<string[] | null>(
    '/api/newapi/fetch-models',
    payload
  )
  return res.data ?? []
}
