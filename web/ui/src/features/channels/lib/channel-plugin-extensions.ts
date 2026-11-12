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
import { CHANNEL_TYPE_NEW_API } from '../constants'
import type { ChannelSettings } from '../types'

// A New API channel proxies an upstream gateway that may host many plugins,
// so the administrator lists the plugins the channel is extended with. The
// single task_plugin_key stays valid there and is shown together with the list.
export function readTaskExtendPluginKeys(
  channelType: number,
  setting:
    | Pick<ChannelSettings, 'task_plugin_key' | 'task_extend_plugin_keys'>
    | null
    | undefined
): string[] {
  if (channelType !== CHANNEL_TYPE_NEW_API || !setting) return []
  const keys = Array.isArray(setting.task_extend_plugin_keys)
    ? setting.task_extend_plugin_keys.filter(
        (key): key is string => typeof key === 'string' && key.trim() !== ''
      )
    : []
  const single =
    typeof setting.task_plugin_key === 'string'
      ? setting.task_plugin_key.trim()
      : ''
  return [...new Set(single ? [single, ...keys] : keys)]
}
