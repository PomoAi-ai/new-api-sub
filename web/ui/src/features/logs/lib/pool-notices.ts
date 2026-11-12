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
import { create } from 'zustand'

import type { PoolNotices } from '../types'

/**
 * The pool notices of the latest response of each log and stats call, by a key
 * such as "logs:list" or "stats:flow". A page shows those under its prefix.
 */
export const usePoolNotices = create<Record<string, PoolNotices>>(() => ({}))

export function reportPoolNotices(key: string, body: PoolNotices) {
  usePoolNotices.setState({
    [key]: { degraded: body.degraded, retention: body.retention },
  })
}
