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
// Modified 2026-11-04 by new-api-sub: from usage-logs/audit/lib/audit-details.ts; reads what this project's actions record in detail
import type { TFunction } from 'i18next'

import { CHANNEL_TYPES } from '@/features/channels/constants'
import { getLogTypeConfig } from '@/features/logs/lib/utils'

import type { AuditLog } from '../api'
import {
  AUDIT_CATEGORIES,
  AUDIT_TARGET_LABELS,
  auditActionLabel,
} from '../constants'

const text = (value: unknown): string =>
  typeof value === 'string' || typeof value === 'number' ? String(value) : ''

/** The method, route and status of the request; rows from before they were recorded have none. */
export function auditRequest(entry: AuditLog) {
  return {
    method: text(entry.detail?.http_method),
    route: text(entry.detail?.http_route),
    status: text(entry.detail?.http_status),
  }
}

/** The name recorded for the target, else its id. */
function targetName(entry: AuditLog): string {
  const name = text(entry.detail?.name) || text(entry.detail?.username)
  if (name) return name
  return entry.target_id ? `#${entry.target_id}` : ''
}

export function auditTargetText(entry: AuditLog, t: TFunction): string {
  if (!entry.target_type) return ''
  const label = t(AUDIT_TARGET_LABELS[entry.target_type] ?? entry.target_type)
  const name = text(entry.detail?.name) || text(entry.detail?.username)
  const id = entry.target_id ? `#${entry.target_id}` : ''
  return [label, name, id].filter(Boolean).join(' ')
}

// What a read was narrowed by, with the labels of the filters that set it.
// Paging and time range are left out: they say nothing about what was looked for.
const QUERY_LABELS: Record<string, string> = {
  model_name: 'Model',
  channel_id: 'Channel',
  type: 'Type',
  username: 'Username',
  token_name: 'Token',
  group: 'Group',
  request_id: 'Request ID',
  upstream_request_id: 'Upstream Request ID',
  user_id: 'User',
  owner: 'View scope',
  action: 'Action',
  category: 'Category',
  success: 'Result',
  actor_type: 'Source',
  actor_name: 'Username',
  actor_id: 'Operator',
  ip: 'IP',
}

function queryValue(key: string, value: string, t: TFunction): string {
  switch (key) {
    case 'type':
      return t(getLogTypeConfig(Number(value)).label)
    case 'owner':
      return t('Only Mine')
    case 'success':
      return value === 'true' ? t('Success') : t('Failed')
    case 'action':
      return t(auditActionLabel(value))
    case 'category':
      return t(
        AUDIT_CATEGORIES.find((item) => item.value === value)?.label ?? value
      )
    default:
      return value
  }
}

function viewText(entry: AuditLog, t: TFunction): string {
  const query = (entry.detail?.query ?? {}) as Record<string, string>
  const conditions = Object.entries(query)
    // type=0 is the "All Types" filter, which narrows nothing.
    .filter(
      ([key, value]) =>
        key in QUERY_LABELS && !(key === 'type' && value === '0')
    )
    .map(
      ([key, value]) => `${t(QUERY_LABELS[key])}=${queryValue(key, value, t)}`
    )
  const label = t(auditActionLabel(entry.action))
  return conditions.length
    ? t('{{event}} ({{conditions}})', {
        event: label,
        conditions: conditions.join(', '),
      })
    : label
}

function channelCreateText(entry: AuditLog, t: TFunction): string {
  const name = targetName(entry)
  const type =
    CHANNEL_TYPES[entry.detail?.type as keyof typeof CHANNEL_TYPES] ?? ''
  const mode = text(entry.detail?.mode)
  const parts = [
    type && t(type),
    mode === 'batch' && t('Batch'),
    mode === 'multi_to_single' && t('Multi-key'),
  ].filter(Boolean)
  return parts.length
    ? t('Add channel {{name}} ({{detail}})', {
        name,
        detail: parts.join(', '),
      })
    : t('Add channel {{name}}', { name })
}

/** Says in a sentence what the entry did, naming its target where it can. */
export function auditEventText(entry: AuditLog, t: TFunction): string {
  const name = targetName(entry)
  const enabled = entry.detail?.to === 1
  switch (entry.action) {
    case 'auth.login':
      return entry.success
        ? t('Sign-in')
        : t('Sign-in failed: user {{name}}', { name: entry.actor_name })
    case 'auth.username_change':
      return entry.detail?.new_username
        ? t('Change the username from {{from}} to {{to}}', {
            from: text(entry.detail.old_username),
            to: text(entry.detail.new_username),
          })
        : t('Username change')
    case 'channel.create':
      return channelCreateText(entry, t)
    case 'channel.create_compensate':
      return t('Roll back creating channel {{name}}', { name })
    case 'channel.status':
      return enabled
        ? t('Enable channel {{name}}', { name })
        : t('Disable channel {{name}}', { name })
    case 'channel.test':
      return entry.detail?.model
        ? t('Test channel {{name}} with {{model}}', {
            name,
            model: text(entry.detail.model),
          })
        : t('Test channel {{name}}', { name })
    case 'channel.balance':
      return t('Refresh the balance of channel {{name}}', { name })
    case 'channel.delete':
      return t('Delete channel {{name}}', { name })
    case 'channel.restore':
      return t('Restore channel {{name}}', { name })
    case 'channel.purge':
      return t('Purge channel {{name}}', { name })
    case 'user.create':
      return t('Create user {{name}}', { name })
    case 'user.status':
      return enabled
        ? t('Enable user {{name}}', { name })
        : t('Disable user {{name}}', { name })
    case 'user.password_reset':
      return t('Reset the password of user {{name}}', { name })
    case 'user.delete':
      return t('Delete user {{name}}', { name })
    case 'admin.create':
      return t('Create admin {{name}}', { name })
    case 'admin.status':
      return enabled
        ? t('Enable admin {{name}}', { name })
        : t('Disable admin {{name}}', { name })
    case 'admin.password_reset':
      return t('Reset the password of admin {{name}}', { name })
    case 'admin.delete':
      return t('Delete admin {{name}}', { name })
    case 'template.create':
      return t('Create template {{name}}', { name })
    case 'template.update':
      return t('Update template {{name}}', { name })
    case 'template.delete':
      return t('Delete template {{name}}', { name })
    case 'log.export':
      return entry.detail?.rows !== undefined
        ? t('Export logs ({{count}} rows)', {
            count: Number(entry.detail.rows),
          })
        : t('Log export')
    default:
      return entry.action.startsWith('view.')
        ? viewText(entry, t)
        : t(auditActionLabel(entry.action))
  }
}
