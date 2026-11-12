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
// Modified 2026-11-04 by new-api-sub: this project's rows: who, what it changed, why it failed, the request and what was asked for; the rest of detail follows as fields
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  DetailRow,
  DetailSection,
} from '@/features/logs/components/dialogs/log-detail-layout'
import dayjs from '@/lib/dayjs'

import type { AuditLog, AuditMode } from '../api'
import {
  auditEventText,
  auditRequest,
  auditTargetText,
} from '../lib/audit-event'
import { AuditDetailFields } from './audit-detail-fields'
import { AuditDetailValue } from './audit-detail-value'

// Keys of detail with a place of their own here, or already in the event's text.
const SHOWN_DETAIL = new Set([
  'http_method',
  'http_route',
  'http_status',
  'query',
  'error',
  'from',
  'to',
  'changes',
  'name',
  'username',
])

export function AuditLogDetailsDialog(props: {
  entry: AuditLog
  mode: AuditMode
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const entry = props.entry
  const detail = entry.detail ?? {}
  const target = auditTargetText(entry, t)
  const request = auditRequest(entry)
  const error = typeof detail.error === 'string' ? detail.error : ''
  const changes = Object.entries(
    (detail.changes ?? {}) as Record<string, unknown>
  )
  const query = Object.entries((detail.query ?? {}) as Record<string, unknown>)
  const extra = Object.entries(detail).filter(([key]) => !SHOWN_DETAIL.has(key))
  // from and to record a status, of which only 1 is enabled.
  const statusText = (value: unknown) =>
    value === 1 ? t('Enabled') : t('Disabled')
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Log Details')}
      description={t('View the complete details for this log entry')}
      descriptionClassName='sr-only'
      trigger={
        <Button variant='ghost' size='sm' className='h-7 px-2'>
          {t('Details')}
        </Button>
      }
      contentClassName='min-w-0 sm:max-w-lg max-sm:max-h-(--dialog-available-height) max-sm:w-[calc(100vw-1.5rem)] max-sm:max-w-[calc(100vw-1.5rem)]'
      titleClassName='text-base'
      contentHeight='auto'
      bodyClassName='space-y-3'
    >
      <div className='min-w-0 space-y-1.5'>
        <p className='text-sm leading-relaxed font-medium break-words'>
          {auditEventText(entry, t)}
        </p>
        <div className='flex flex-wrap items-center gap-2 text-xs'>
          <StatusBadge
            label={entry.success ? t('Success') : t('Failed')}
            variant={entry.success ? 'success' : 'danger'}
            copyable={false}
          />
          <span className='text-muted-foreground tabular-nums'>
            {dayjs.unix(entry.created_at).format('YYYY-MM-DD HH:mm:ss')}
          </span>
        </div>
      </div>
      <DetailSection label={t('Operation Audit Info')}>
        {props.mode === 'admin' && (
          <DetailRow
            label={t('Operator')}
            value={[
              entry.actor_type === 'admin' ? t('Admin') : t('Portal'),
              entry.actor_name,
              entry.actor_id ? `#${entry.actor_id}` : '',
            ]
              .filter(Boolean)
              .join(' ')}
          />
        )}
        <DetailRow
          label={t('Action')}
          value={
            <AuditDetailValue label={t('Action')} value={entry.action} mono />
          }
        />
        {target && <DetailRow label={t('Target')} value={target} />}
        {detail.from !== undefined && (
          <DetailRow label={t('Before')} value={statusText(detail.from)} />
        )}
        {detail.to !== undefined && (
          <DetailRow label={t('After')} value={statusText(detail.to)} />
        )}
        {error && (
          <DetailRow
            label={t('Failure reason')}
            value={
              <AuditDetailValue label={t('Failure reason')} value={error} />
            }
          />
        )}
      </DetailSection>
      {changes.length > 0 && (
        <DetailSection label={t('Changes')}>
          <AuditDetailFields
            fields={changes.map(([label, value]) => ({ label, value }))}
          />
        </DetailSection>
      )}
      <DetailSection label={t('Request')}>
        {request.method && (
          <DetailRow label={t('Method')} value={request.method} mono />
        )}
        {request.route && (
          <DetailRow
            label={t('Route')}
            value={
              <AuditDetailValue
                label={t('Route')}
                value={request.route}
                mono
                copyable
              />
            }
          />
        )}
        {request.status && (
          <DetailRow label='HTTP' value={request.status} mono />
        )}
        <DetailRow label='IP' value={entry.ip || '—'} mono />
        {entry.forwarded_for && (
          <DetailRow
            label='X-Forwarded-For'
            value={
              <AuditDetailValue
                label='X-Forwarded-For'
                value={entry.forwarded_for}
                mono
                copyable
              />
            }
          />
        )}
        {entry.user_agent && (
          <DetailRow
            label={t('Client')}
            value={
              <AuditDetailValue
                label={t('User Agent')}
                value={entry.user_agent}
                mono
                copyable
              />
            }
          />
        )}
      </DetailSection>
      {query.length > 0 && (
        <DetailSection label={t('Query parameters')}>
          <AuditDetailFields
            fields={query.map(([label, value]) => ({ label, value }))}
          />
        </DetailSection>
      )}
      {extra.length > 0 && (
        <DetailSection label={t('Additional information')}>
          <AuditDetailFields
            fields={extra.map(([label, value]) => ({ label, value }))}
          />
        </DetailSection>
      )}
    </Dialog>
  )
}
