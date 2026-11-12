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
import { withProof } from '@/features/account/security/api'
import { api } from '@/lib/api'

export type AppSettings = {
  log_scope: 'all' | 'self'
  portal_name: string
  admin_name: string
  portal_public_url: string
  /** Per-key test and balance of multi-key channels, read on the server. */
  key_probe: boolean
}

export const settingsQueryKey = ['admin-settings'] as const

export async function getSettings(): Promise<AppSettings> {
  return (await api.get<AppSettings>('/api/settings')).data
}

export async function updateSettings(body: AppSettings): Promise<AppSettings> {
  return (await api.put<AppSettings>('/api/settings', body)).data
}

type ConnectionDatabase = {
  set: boolean
  /** host:port/db, without the user or password. */
  host: string
}

export type PoolLogMode = 'direct' | 'sync'

/** How often and how much the log sync reads; PUT /api/pools/{id}/sync. */
export type PoolSyncControl = {
  enabled: boolean
  /** Seconds between rounds, 30–3600. */
  interval_s: number
  /** Seconds of logs per request, 60–3600. */
  window_s: number
  /** Requests per round at most, 1–60. */
  max_windows: number
}

/**
 * What the log sync worker reports; null until sync is set up for the pool.
 * Times are Unix seconds, null or 0 for never.
 */
export type PoolSync = PoolSyncControl & {
  /** Every log before it has been synced. */
  cursor: number | null
  /** How far the cursor is behind now, in seconds. */
  lag_s: number | null
  newest_log_at: number | null
  last_run_at: number | null
  last_ok_at: number | null
  last_rows: number
  last_ms: number
  rows_total: number
  last_error: string
  backoff_until: number | null
  /** The conflict kind that paused the worker; empty while running. */
  paused: SyncConflictKind | ''
  open_conflicts: number
}

export type SyncConflictKind =
  | 'fingerprint'
  | 'id_regression'
  | 'duplicate_source'
  | 'zero_id'
  | 'clock_skew'

/**
 * What each conflict kind means and what to do about it, as i18n keys; all
 * but clock_skew pause the sync.
 */
export const CONFLICT_KINDS: Record<
  SyncConflictKind,
  { label: string; explain: string }
> = {
  fingerprint: {
    label: 'Same log id, different content',
    explain:
      'The pool sent a log whose id is already stored with different content. The stored copy is kept and the new one dropped. Check whether the new-api database was restored or replaced before continuing.',
  },
  id_regression: {
    label: 'Log ids went backwards',
    explain:
      'Newer logs came with smaller ids than those already synced: the new-api log table was probably cleared or its database replaced. Continuing starts a new id baseline from the next window; if the database was replaced, clear and resync instead.',
  },
  duplicate_source: {
    label: 'Same new-api configured twice',
    explain:
      'Logs of this pool are also stored under another pool, so both probably point at the same new-api. Remove or fix the duplicate pool before continuing, or the logs are counted twice.',
  },
  zero_id: {
    label: 'Logs without an id',
    explain:
      'The pool sent logs with id 0 (as a ClickHouse log store does), which cannot be deduplicated. The sync pauses again on every such log; switch the pool to reading its database.',
  },
  clock_skew: {
    label: 'Clock difference',
    explain:
      'The clock of the new-api server differs from ours by more than 60 seconds, so logs written late may be missed. A warning only: the sync goes on.',
  },
}

/** A conflict the sync found. detail never holds log content. */
export type SyncConflict = {
  id: number
  pool_id: number
  kind: SyncConflictKind
  log_id: number
  detail: Record<string, unknown> | null
  count: number
  first_seen: number
  last_seen: number
  resolved_by: string
  /** 0 or null while open. */
  resolved_at: number | null
}

/** Usage of one window as the pool's database and MongoDB have it. */
export type PoolSyncVerify = {
  start: number
  end: number
  db: { count: number; quota: number }
  mongo: { count: number; quota: number }
}

/** A row of GET /api/pools. Secrets come back as "set" flags only. */
export type Pool = {
  id: number
  name: string
  base_url: string
  log_mode: PoolLogMode
  /** False: new channels skip this pool; existing ones are kept. */
  accept_new: boolean
  pat_set: boolean
  db: ConnectionDatabase
  log_db: ConnectionDatabase
  key_db: ConnectionDatabase
  /** As this console sees it; reason says why not. */
  available: boolean
  reason: string
  quota_per_unit: number | null
  /** Channels placed in this pool and templates that pick it. */
  placements: number
  templates: number
  version: number
  updated_by: string
  updated_at: number
  sync: PoolSync | null
}

/** Empty secrets keep the saved value. Never put this in a query or mutation. */
export type PoolInput = {
  name: string
  base_url: string
  log_mode: PoolLogMode
  accept_new: boolean
  pat: string
  db_dsn: string
  log_dsn: string
  key_dsn: string
  clear_log_dsn: boolean
  clear_key_dsn: boolean
  confirm_address_change: boolean
}

export type PoolTestResult = {
  ok: boolean
  steps: { name: string; ok: boolean; message?: string }[]
}

export const poolsQueryKey = ['admin-pools'] as const

export async function getPools(): Promise<Pool[]> {
  return (await api.get<Pool[] | null>('/api/pools')).data ?? []
}

/** id: the saved pool whose secrets fill the empty fields; 0 for a new one. */
export async function testPool(
  id: number,
  body: PoolInput
): Promise<PoolTestResult> {
  return (await api.post<PoolTestResult>('/api/pools/test', { ...body, id }))
    .data
}

export async function createPool(
  body: PoolInput,
  proof: string
): Promise<Pool> {
  return (await api.post<Pool>('/api/pools', body, withProof(proof))).data
}

export async function updatePool(
  id: number,
  body: PoolInput,
  proof: string
): Promise<Pool> {
  return (await api.put<Pool>(`/api/pools/${id}`, body, withProof(proof))).data
}

export async function deletePool(id: number, proof: string) {
  await api.delete(`/api/pools/${id}`, withProof(proof))
}

/** backfill_days: how far back a sync that never ran starts, 0–30. */
export async function updatePoolSync(
  id: number,
  body: PoolSyncControl & { backfill_days: number }
) {
  await api.put(`/api/pools/${id}/sync`, body)
}

/** Pools with a database: the last hours as both sides count them. */
export async function verifyPoolSync(
  id: number,
  hours = 24
): Promise<PoolSyncVerify> {
  return (
    await api.get<PoolSyncVerify>(`/api/pools/${id}/sync/verify`, {
      params: { hours },
    })
  ).data
}

/** Deletes the pool's synced logs and starts over backfill_days back. */
export async function resetPoolSync(
  id: number,
  backfillDays: number,
  proof: string
) {
  await api.post(
    `/api/pools/${id}/sync/reset`,
    { backfill_days: backfillDays },
    withProof(proof)
  )
}

export async function getPoolConflicts(id: number): Promise<SyncConflict[]> {
  return (
    (await api.get<SyncConflict[] | null>(`/api/pools/${id}/conflicts`)).data ??
    []
  )
}

/** resumed: no conflict that pauses the sync is left open, so it runs again. */
export async function resolvePoolConflict(
  poolId: number,
  id: number
): Promise<{ resumed?: boolean }> {
  return (
    (
      await api.post<{ resumed?: boolean } | null>(
        `/api/pools/${poolId}/conflicts/resolve`,
        { id }
      )
    ).data ?? {}
  )
}

export async function uploadLogo(file: Blob, contentType: string) {
  await api.put('/api/settings/logo', file, {
    headers: { 'Content-Type': contentType },
  })
}

export async function deleteLogo() {
  await api.delete('/api/settings/logo')
}
