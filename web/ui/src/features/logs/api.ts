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
// Modified 2026-11-08 by new-api-sub: this project's /api/logs routes; the server limits every one to the caller's channels and hides the end user from all but a super; pool notices of a response
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import type { UsageLog } from './data/schema'
import { reportPoolNotices } from './lib/pool-notices'
import type { GetLogsParams, LogStatistics, PoolNotices } from './types'

/** The server's names for the filter the page builds in new-api's terms. */
function logQuery(params: GetLogsParams) {
  const { channel, start_timestamp, end_timestamp, ...rest } = params
  return {
    ...rest,
    start: start_timestamp,
    end: end_timestamp,
    channel_id: channel || undefined,
  }
}

export async function getLogs(
  params: GetLogsParams
): Promise<{ items: UsageLog[]; total: number }> {
  const res = await api.get<
    {
      items: (Omit<UsageLog, 'channel'> & { channel_id: number })[]
      total: number
    } & PoolNotices
  >('/api/logs', { params: logQuery(params) })
  reportPoolNotices('logs:list', res.data)
  return {
    total: res.data.total,
    items: res.data.items.map(({ channel_id, ...log }) => ({
      ...log,
      channel: channel_id,
    })),
  }
}

export async function getLogStats(
  params: GetLogsParams
): Promise<LogStatistics> {
  const res = await api.get<LogStatistics>('/api/logs/stat', {
    params: logQuery(params),
  })
  reportPoolNotices('logs:stat', res.data)
  return res.data
}

/** new-api's list; super only. */
export async function getGroups(): Promise<string[]> {
  const res = await api.get<string[]>('/api/newapi/groups')
  return res.data
}

/** The groups channels serve, for callers who may not read new-api's list. */
export async function getLogGroups(): Promise<string[]> {
  const res = await api.get<{ groups: string[] }>('/api/logs/groups')
  return res.data.groups
}

/**
 * Reads the CSV of GET /api/logs/export as it arrives. The server breaks the
 * connection when it fails midway, which rejects here: a file is only ever made
 * from a complete response.
 */
export async function exportLogs(
  params: GetLogsParams,
  options: {
    lang: 'zh' | 'en'
    signal: AbortSignal
    onProgress: (count: number) => void
  }
): Promise<{ parts: Uint8Array<ArrayBuffer>[]; count: number }> {
  const url = api.getUri({
    url: '/api/logs/export',
    params: {
      ...logQuery(params),
      lang: options.lang,
      // seconds east of UTC, so the file's times read as they do on the page
      tz_offset: -new Date().getTimezoneOffset() * 60,
    },
  })
  const response = await fetch(url, {
    credentials: 'include',
    signal: options.signal,
  })
  if (!response.ok || !response.body) {
    const body: unknown = await response.json().catch(() => null)
    throw createServerError(body ?? { status: response.status })
  }
  const reader = response.body.getReader()
  const parts: Uint8Array<ArrayBuffer>[] = []
  let lines = 0
  let quoted = false // every cell is quoted, and a line break inside one is not a row
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    parts.push(value)
    for (const byte of value) {
      if (byte === 0x22) quoted = !quoted
      else if (byte === 0x0a && !quoted) lines++
    }
    options.onProgress(Math.max(lines - 1, 0))
  }
  return { parts, count: Math.max(lines - 1, 0) } // less the header
}

/** Row of GET /api/channels; owner exists only in the admin app. */
export interface ChannelOption {
  id: number
  name: string
  owner?: { user_id: number; username: string }
  placements?: { pool_id: number; pool_name: string }[] | null
}

export async function getChannelOptions(): Promise<ChannelOption[]> {
  const res = await api.get('/api/channels')
  return res.data
}

export async function getPoolNames(): Promise<{ id: number; name: string }[]> {
  const res = await api.get('/api/pool-names')
  return res.data
}

export async function getUserOptions(): Promise<
  { id: number; username: string }[]
> {
  const res = await api.get('/api/users')
  return res.data
}
