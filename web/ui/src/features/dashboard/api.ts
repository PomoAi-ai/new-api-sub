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
// Modified 2026-11-08 by new-api-sub: this project's /api/stats routes, all limited by the server to the caller's channels; pool notices of a response; totals per pool, channel and flow rows per pool
import { reportPoolNotices } from '@/features/logs/lib/pool-notices'
import type { PoolNotices } from '@/features/logs/types'
import { api } from '@/lib/api'

import type {
  ChannelQuotaDataItem,
  FlowQuotaDataItem,
  PoolQuotaDataItem,
  QuotaDataItem,
  StatsQueryParams,
} from './types'

// The backend reads repeated channel_id=1&channel_id=2, not channel_id[]=.
const paramsSerializer = { indexes: null }

type Rows<T> = { rows: T[] } & PoolNotices

export async function getUserQuotaDates(
  params: StatsQueryParams
): Promise<QuotaDataItem[]> {
  const res = await api.get<Rows<QuotaDataItem>>('/api/stats', {
    params,
    paramsSerializer,
  })
  reportPoolNotices('stats:models:quota', res.data)
  return res.data.rows
}

/** splitPool: a row per pool a channel is in, rather than one per channel. */
export async function getChannelQuotaData(
  params: StatsQueryParams,
  splitPool = false
): Promise<ChannelQuotaDataItem[]> {
  const res = await api.get<Rows<ChannelQuotaDataItem>>('/api/stats', {
    params: { ...params, by: 'channel', split: splitPool ? 'pool' : undefined },
    paramsSerializer,
  })
  reportPoolNotices('stats:models:channel', res.data)
  return res.data.rows
}

/** Each pool's total over the range, biggest first. */
export async function getPoolQuotaData(
  params: StatsQueryParams
): Promise<PoolQuotaDataItem[]> {
  const res = await api.get<Rows<PoolQuotaDataItem>>('/api/stats', {
    params: { ...params, by: 'pool' },
    paramsSerializer,
  })
  reportPoolNotices('stats:models:pool', res.data)
  return res.data.rows
}

export async function getUserQuotaDataByUsers(params: {
  start_timestamp: number
  end_timestamp: number
  pool_id?: number
}): Promise<QuotaDataItem[]> {
  const res = await api.get<Rows<QuotaDataItem>>('/api/stats/users', {
    params: {
      start: params.start_timestamp,
      end: params.end_timestamp,
      pool_id: params.pool_id,
    },
  })
  reportPoolNotices('stats:users', res.data)
  return res.data.rows
}

/** Rows per pool: the flow's last column is the pool. */
export async function getFlowQuotaDates(
  params: StatsQueryParams
): Promise<FlowQuotaDataItem[]> {
  const res = await api.get<Rows<FlowQuotaDataItem>>('/api/stats/flow', {
    params: { ...params, split: 'pool' },
    paramsSerializer,
  })
  reportPoolNotices('stats:flow', res.data)
  return res.data.rows
}
