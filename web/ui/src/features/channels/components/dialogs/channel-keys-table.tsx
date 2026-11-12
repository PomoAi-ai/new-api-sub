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
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Checkbox } from '@/components/ui/checkbox'
import { formatTimestampToDate } from '@/lib/format'

import { formatKeyBalance } from '../../lib'
import type { ChannelKey } from '../../types'
import { KeyRowActions, KeyStatusBadge, KeyTestCell } from './channel-key-cells'
import type {
  KeyActionProgress,
  KeyTestOutcome,
} from './use-channel-key-actions'

/** Only the manage variant has these. */
export type ChannelKeysTableManage = {
  canProbe: boolean
  model: string
  testResults: ReadonlyMap<number, KeyTestOutcome>
  progress: KeyActionProgress | null
  selected: ReadonlySet<number>
  allSelected: boolean
  someSelected: boolean
  onSelectAll: (checked: boolean) => void
  onSelect: (index: number, checked: boolean) => void
  onToggle: (key: ChannelKey) => void
  onTest: (index: number) => void
  onQueryBalance: (index: number) => void
}

export function ChannelKeysTable(props: {
  keys: ChannelKey[]
  manage?: ChannelKeysTableManage
}) {
  const { t } = useTranslation()
  const { manage } = props
  const columns: StaticDataTableColumn<ChannelKey>[] = []
  if (manage) {
    columns.push({
      id: 'select',
      header: (
        <Checkbox
          checked={manage.allSelected}
          indeterminate={manage.someSelected}
          onCheckedChange={(checked) => manage.onSelectAll(checked)}
          aria-label={t('Select all')}
        />
      ),
      className: 'w-10',
      cell: (key) => (
        <Checkbox
          checked={manage.selected.has(key.index)}
          onCheckedChange={(checked) => manage.onSelect(key.index, checked)}
          aria-label={t('Select key #{{n}}', { n: key.index + 1 })}
        />
      ),
    })
  }
  columns.push(
    {
      id: 'index',
      header: t('Key #'),
      className: 'w-20',
      cellClassName: 'font-mono text-sm',
      cell: (key) => `#${key.index + 1}`,
    },
    {
      id: 'status',
      header: t('Status'),
      className: 'w-28',
      cell: (key) => <KeyStatusBadge status={key.status} />,
    }
  )
  if (manage) {
    columns.push({
      id: 'disabled',
      header: t('Disabled Time / Reason'),
      cellClassName: 'whitespace-normal',
      cell: (key) =>
        key.disabled_time || key.reason ? (
          <div className='space-y-0.5 text-sm'>
            <div className='text-muted-foreground'>
              {formatTimestampToDate(key.disabled_time ?? 0)}
            </div>
            {key.reason && (
              <div className='line-clamp-2 break-all' title={key.reason}>
                {key.reason}
              </div>
            )}
          </div>
        ) : (
          '-'
        ),
    })
  }
  columns.push({
    id: 'balance',
    header: t('Balance'),
    className: manage ? 'w-40' : undefined,
    cellClassName: 'whitespace-normal',
    cell: (key) => (
      <div className='space-y-0.5 text-sm'>
        <div className='font-mono tabular-nums'>
          {key.balance == null ? '-' : formatKeyBalance(key.balance)}
        </div>
        {key.balance_at ? (
          <div className='text-muted-foreground text-xs'>
            {formatTimestampToDate(key.balance_at)}
          </div>
        ) : null}
        {key.balance_error ? (
          <div
            className='text-destructive line-clamp-2 text-xs break-all'
            title={key.balance_error}
          >
            {key.balance_error}
          </div>
        ) : null}
      </div>
    ),
  })
  if (manage?.canProbe) {
    columns.push({
      id: 'test',
      header: t('Test Result'),
      cell: (key) => (
        <KeyTestCell outcome={manage.testResults.get(key.index)} />
      ),
    })
  }
  if (manage) {
    columns.push({
      id: 'actions',
      header: t('Actions'),
      className: 'w-28 text-right',
      cellClassName: 'text-right',
      cell: (key) => <KeyRowActions channelKey={key} manage={manage} />,
    })
  }

  // The fixed layout keeps the actions column in view whatever a cell holds.
  return (
    <StaticDataTable
      className='[&_thead]:bg-popover max-h-[50dvh] overflow-auto [&_thead]:sticky [&_thead]:top-0 [&_thead]:z-10'
      tableClassName={manage ? 'min-w-[960px] table-fixed' : undefined}
      tableProps={{ withContainer: false }}
      data={props.keys}
      getRowKey={(key) => key.index}
      columns={columns}
      emptyContent={t('No keys found')}
    />
  )
}
