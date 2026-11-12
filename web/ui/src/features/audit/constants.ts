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
// Modified 2026-11-08 by new-api-sub: this project's actions and categories, and who can come across each
import type { AuditMode } from './api'

interface AuditOption {
  value: string
  label: string
  /** In the portal's own records. */
  portal: boolean
  /** Only a super can do this, so no one else's records hold it. */
  superOnly?: boolean
}

// Action names recorded by internal/audit; labels are i18n keys.
export const AUDIT_ACTIONS: AuditOption[] = [
  { value: 'auth.login', label: 'Sign-in', portal: true },
  {
    value: 'auth.login_pending',
    label: 'Sign-in awaiting second step',
    portal: true,
  },
  { value: 'auth.2fa_failed', label: 'Second step failure', portal: true },
  { value: 'auth.captcha_failed', label: 'Captcha failure', portal: true },
  { value: 'auth.rate_limited', label: 'Sign-in rate limited', portal: true },
  { value: 'auth.logout', label: 'Sign-out', portal: true },
  { value: 'auth.proof', label: 'Identity verification', portal: true },
  { value: 'auth.totp_bind', label: 'Authenticator binding', portal: true },
  { value: 'auth.totp_unbind', label: 'Authenticator removal', portal: true },
  { value: 'auth.passkey_bind', label: 'Passkey binding', portal: true },
  { value: 'auth.passkey_unbind', label: 'Passkey removal', portal: true },
  {
    value: 'auth.passkey_clone_warning',
    label: 'Passkey clone warning',
    portal: true,
  },
  {
    value: 'auth.backup_regenerate',
    label: 'Backup code regeneration',
    portal: true,
  },
  { value: 'auth.password_change', label: 'Password change', portal: true },
  { value: 'auth.token_create', label: 'Token creation', portal: true },
  { value: 'auth.token_delete', label: 'Token revocation', portal: true },
  {
    value: 'auth.username_change',
    label: 'Username change',
    portal: false,
    superOnly: true,
  },
  { value: 'channel.create', label: 'Channel creation', portal: true },
  {
    value: 'channel.create_compensate',
    label: 'Channel creation rollback',
    portal: true,
  },
  { value: 'channel.status', label: 'Channel status change', portal: true },
  { value: 'channel.test', label: 'Channel test', portal: true },
  { value: 'channel.balance', label: 'Channel balance refresh', portal: true },
  { value: 'channel.delete', label: 'Channel deletion', portal: true },
  { value: 'channel.restore', label: 'Channel restore', portal: false },
  { value: 'channel.purge', label: 'Channel purge', portal: false },
  { value: 'channel.push', label: 'Channel push to a pool', portal: false },
  {
    value: 'channel.placement_retry',
    label: 'Channel push retry',
    portal: false,
  },
  {
    value: 'channel.placement_cancel',
    label: 'Channel push cancel',
    portal: false,
    superOnly: true,
  },
  {
    value: 'channel.fetch_models',
    label: 'Upstream model fetch',
    portal: false,
    superOnly: true,
  },
  {
    value: 'admin.create',
    label: 'Admin creation',
    portal: false,
    superOnly: true,
  },
  {
    value: 'admin.status',
    label: 'Admin status change',
    portal: false,
    superOnly: true,
  },
  {
    value: 'admin.password_reset',
    label: 'Admin password reset',
    portal: false,
    superOnly: true,
  },
  {
    value: 'admin.2fa_reset',
    label: 'Admin two-step reset',
    portal: false,
    superOnly: true,
  },
  {
    value: 'admin.delete',
    label: 'Admin deletion',
    portal: false,
    superOnly: true,
  },
  { value: 'user.create', label: 'User creation', portal: false },
  { value: 'user.status', label: 'User status change', portal: false },
  { value: 'user.password_reset', label: 'User password reset', portal: false },
  { value: 'user.2fa_reset', label: 'User two-step reset', portal: false },
  { value: 'user.delete', label: 'User deletion', portal: false },
  {
    value: 'template.create',
    label: 'Template creation',
    portal: false,
    superOnly: true,
  },
  {
    value: 'template.update',
    label: 'Template update',
    portal: false,
    superOnly: true,
  },
  {
    value: 'template.delete',
    label: 'Template deletion',
    portal: false,
    superOnly: true,
  },
  {
    value: 'settings.newapi_update',
    label: 'Connection update',
    portal: false,
    superOnly: true,
  },
  {
    value: 'settings.newapi_import',
    label: 'Connection import',
    portal: false,
    superOnly: true,
  },
  ...(
    [
      ['pool.create', 'Pool creation'],
      ['pool.update', 'Pool update'],
      ['pool.delete', 'Pool deletion'],
      ['pool.test', 'Pool connection test'],
      ['pool.sync_update', 'Pool sync settings update'],
      ['pool.sync_reset', 'Pool sync reset'],
      ['pool.conflict_resolve', 'Pool sync conflict resolution'],
    ] as const
  ).map(([value, label]) => ({
    value,
    label,
    portal: false,
    superOnly: true,
  })),
  { value: 'log.export', label: 'Log export', portal: true },
  { value: 'view.channel_types', label: 'View channel types', portal: false },
  { value: 'view.channels', label: 'View channels', portal: false },
  { value: 'view.logs', label: 'View logs', portal: false },
  { value: 'view.log_stat', label: 'View log totals', portal: false },
  { value: 'view.log_groups', label: 'View log groups', portal: false },
  { value: 'view.stats', label: 'View statistics', portal: false },
  {
    value: 'view.stats_perf',
    label: 'View performance statistics',
    portal: false,
  },
  { value: 'view.stats_flow', label: 'View flow statistics', portal: false },
  { value: 'view.stats_users', label: 'View user statistics', portal: false },
  { value: 'view.audit_logs', label: 'View audit logs', portal: false },
  { value: 'view.users', label: 'View users', portal: false },
  { value: 'view.admins', label: 'View admins', portal: false },
  { value: 'view.templates', label: 'View templates', portal: false },
  { value: 'view.api_tokens', label: 'View API tokens', portal: false },
  { value: 'view.newapi', label: 'Read new-api data', portal: false },
]

// The ?category= values of GET /api/audit-logs.
export const AUDIT_CATEGORIES: AuditOption[] = [
  { value: 'auth', label: 'Account', portal: true },
  { value: 'channel', label: 'Channel', portal: true },
  { value: 'user', label: 'User', portal: false },
  { value: 'admin', label: 'Admin', portal: false, superOnly: true },
  { value: 'template', label: 'Template', portal: false, superOnly: true },
  { value: 'log', label: 'Logs', portal: true },
  { value: 'view', label: 'Access', portal: false },
]

/** The options that can match anything for this viewer. */
export function auditOptionsFor(
  options: AuditOption[],
  mode: AuditMode,
  isSuper: boolean
): AuditOption[] {
  return options.filter((option) =>
    mode === 'portal' ? option.portal : isSuper || !option.superOnly
  )
}

export function auditActionLabel(action: string): string {
  return AUDIT_ACTIONS.find((item) => item.value === action)?.label ?? action
}

export const AUDIT_TARGET_LABELS: Record<string, string> = {
  admin: 'Admin',
  user: 'User',
  template: 'Template',
  channel: 'Channel',
  api_token: 'Token',
  pool: 'Pool',
  session: 'Session',
  log: 'Logs',
}
