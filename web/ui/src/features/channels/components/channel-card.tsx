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
// Modified 2026-11-08 by new-api-sub: no selection, tag rows or drag handle; adds the owner line for admins, the push state until every pool has the channel, and keeps the status badge where no power toggle is shown
import { flexRender, type Row } from '@tanstack/react-table'
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import { GroupBadge } from '@/components/group-badge'
import { StatusBadgeTypeContext } from '@/components/status-badge'

import { CHANNEL_STATUS } from '../constants'
import { parseGroupsList } from '../lib'
import type { Channel } from '../types'
import { ChannelRowActionsLayoutContext } from './channel-row-actions-context'
import { useChannels } from './channels-provider'

const SENSITIVE_MASK = '••••'

/**
 * Bespoke channel card for the card view. Reuses every column's existing cell
 * renderer via `flexRender`, so the table's information and interactions are
 * preserved: provider/multi-key type badge, id, name, status (with tooltips),
 * groups, priority/weight, balance refresh, response/test times, and the
 * per-row actions menu.
 */
function ChannelCardComponent({
  row,
  isSelected,
}: {
  row: Row<Channel>
  isSelected: boolean
}) {
  const { t } = useTranslation()
  const { mode, sensitiveVisible } = useChannels()
  const cells = row.getAllCells()

  const renderCell = (id: string) => {
    const cell = cells.find((c) => c.column.id === id)
    if (!cell || !cell.column.columnDef.cell) {
      return null
    }
    return flexRender(cell.column.columnDef.cell, cell.getContext())
  }

  const groups = parseGroupsList(row.original.group ?? '')

  const typeCell = renderCell('type')
  const nameCell = renderCell('name')
  const statusCell = renderCell('status')
  const pushCell =
    row.original.push_state === 'done' ? null : renderCell('push_state')
  const actionsCell = renderCell('actions')
  const priorityCell = renderCell('priority')
  const weightCell = renderCell('weight')
  const balanceCell = renderCell('balance')
  const responseCell = renderCell('response_time')
  const testCell = renderCell('test_time')
  const ownerCell = mode === 'portal' ? null : renderCell('owner')

  const labelClass = 'text-muted-foreground text-[11px] font-medium select-none'

  // In card view the enable/disable state is already conveyed by the inline
  // power toggle, so the plain "Enabled"/"Disabled" badge is redundant. Keep
  // only the informative states (e.g. auto-disabled, unknown, deleted upstream)
  // and every row without a toggle.
  const showStatusBadge =
    !row.original.exists ||
    Boolean(row.original.deleted_at) ||
    Boolean(row.original.status_pending) ||
    (row.original.status !== CHANNEL_STATUS.ENABLED &&
      row.original.status !== CHANNEL_STATUS.MANUAL_DISABLED)

  return (
    <ChannelRowActionsLayoutContext.Provider value='card'>
      <div
        data-state={isSelected ? 'selected' : undefined}
        className='flex min-w-0 flex-col gap-3'
      >
        {/* Row 1: type, with status badge + actions menu */}
        <div className='flex items-center justify-between gap-2'>
          <div className='flex min-w-0 flex-1 items-center gap-2'>
            <div className='min-w-0 overflow-hidden'>{typeCell}</div>
          </div>
          <div className='flex shrink-0 items-center gap-1.5'>
            {pushCell}
            {showStatusBadge && statusCell}
            {actionsCell}
          </div>
        </div>

        {/* Both rows share their columns: identity/balance on the left,
          priority/response and weight/last tested on the right. */}
        <StatusBadgeTypeContext.Provider value='text'>
          <div className='grid grid-cols-[minmax(0,1fr)_auto_auto] items-start gap-3'>
            <div className='min-w-0 overflow-hidden text-sm'>
              <div className={labelClass}>
                #{sensitiveVisible ? row.original.id : SENSITIVE_MASK}
              </div>
              {nameCell}
            </div>
            <div
              data-column-id='priority'
              className='flex min-w-0 flex-col gap-1'
            >
              <span className={labelClass}>{t('Priority')}</span>
              {priorityCell}
            </div>
            <div className='flex min-w-0 flex-col gap-1'>
              <span className={labelClass}>{t('Weight')}</span>
              {weightCell}
            </div>
            <dl className='col-span-3 grid grid-cols-subgrid gap-y-1'>
              <div className='row-span-2 grid min-w-0 grid-rows-subgrid'>
                <dt className={labelClass}>{t('Used / Remaining')}</dt>
                <dd className='min-w-0 text-sm tabular-nums [&_[data-slot=status-badge]]:!ml-0 [&>div]:ml-0 [&>div]:flex-wrap [&>div]:gap-x-3'>
                  {balanceCell ?? (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </dd>
              </div>
              <div className='row-span-2 grid min-w-0 grid-rows-subgrid'>
                <dt className={labelClass}>{t('Response')}</dt>
                <dd className='min-w-0 text-sm tabular-nums [&_[data-slot=status-badge]]:!ml-0'>
                  {responseCell ?? (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </dd>
              </div>
              <div className='row-span-2 grid min-w-0 grid-rows-subgrid'>
                <dt className={labelClass}>{t('Last Tested')}</dt>
                <dd className='min-w-0 text-sm [&_[data-slot=status-badge]]:!ml-0'>
                  {testCell ?? <span className='text-muted-foreground'>-</span>}
                </dd>
              </div>
            </dl>
            {ownerCell && (
              <div className='col-span-3 min-w-0 text-sm'>
                <div className={labelClass}>{t('Channel Owner')}</div>
                {ownerCell}
              </div>
            )}
          </div>
        </StatusBadgeTypeContext.Provider>

        {/* Groups retain their compact, full-width footer. */}
        <div className='min-w-0'>
          {groups.length > 0 ? (
            <div className='-ml-1.5 flex min-w-0 flex-wrap gap-1'>
              {groups.map((g) => (
                <GroupBadge
                  key={g}
                  group={g}
                  label={sensitiveVisible ? undefined : SENSITIVE_MASK}
                  size='sm'
                />
              ))}
            </div>
          ) : (
            <span className='text-muted-foreground text-sm'>-</span>
          )}
        </div>
      </div>
    </ChannelRowActionsLayoutContext.Provider>
  )
}

/**
 * Memoized so each card only re-renders when its own react-table row reference
 * changes, instead of every card re-rendering whenever the parent table state
 * (filters, pagination, sensitive toggle, etc.) updates.
 */
export const ChannelCard = memo(ChannelCardComponent)
