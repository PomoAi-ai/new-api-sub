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

import { TimestampCell } from '@/components/activity-time-cell'
import { EmptyState } from '@/components/empty-state'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import type { SettlementOverview as Overview } from '../api'
import { formatUsd } from '../lib'

/** Run, settled and pending in USD, and the records with the newest first. */
export function SettlementOverview(props: {
  overview: Overview
  showRecordedBy?: boolean
}) {
  const { t } = useTranslation()
  const { overview } = props
  const stats = [
    { label: t('Run'), value: overview.used_usd },
    { label: t('Settled'), value: overview.settled_usd },
    { label: t('Pending settlement'), value: overview.pending_usd },
  ]

  return (
    <div className='space-y-4'>
      <div className='divide-border/60 grid grid-cols-3 divide-x overflow-hidden rounded-lg border'>
        {stats.map((stat) => (
          <div key={stat.label} className='min-w-0 px-3 py-3 sm:px-4'>
            <div className='text-muted-foreground text-xs'>{stat.label}</div>
            <div className='mt-1 truncate text-lg font-semibold tabular-nums'>
              {formatUsd(stat.value)}
            </div>
          </div>
        ))}
      </div>
      {overview.items.length === 0 ? (
        <EmptyState
          className='min-h-0 rounded-lg border p-4'
          title={t('No settlement records yet')}
        />
      ) : (
        <div className='overflow-hidden rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Settled up to')}</TableHead>
                <TableHead>{t('Note')}</TableHead>
                {props.showRecordedBy && (
                  <TableHead>{t('Recorded by')}</TableHead>
                )}
                <TableHead>{t('Created At')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {overview.items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className='tabular-nums'>
                    {formatUsd(item.amount_usd)}
                  </TableCell>
                  <TableCell className='max-w-64 whitespace-pre-wrap'>
                    {item.note}
                  </TableCell>
                  {props.showRecordedBy && (
                    <TableCell>{item.created_by}</TableCell>
                  )}
                  <TableCell>
                    <TimestampCell
                      timestamp={item.created_at}
                      format='absolute'
                      justNowLabel={t('Just now')}
                      className='text-muted-foreground text-xs'
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
