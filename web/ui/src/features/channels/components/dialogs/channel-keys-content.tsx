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
import type { ColumnDef } from '@tanstack/react-table'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DataTablePagination, useDataTable } from '@/components/data-table'
import { Alert, AlertDescription } from '@/components/ui/alert'

import { CHANNEL_STATUS } from '../../constants'
import { parseModelsString } from '../../lib'
import type { Channel, ChannelKey, ChannelKeys } from '../../types'
import { ChannelKeysBulkBar } from './channel-keys-bulk-bar'
import { ChannelKeysTable } from './channel-keys-table'
import { ChannelKeysStats, ChannelKeysToolbar } from './channel-keys-toolbar'
import { useChannelKeyActions } from './use-channel-key-actions'

/** manage: every action; balance: balances only; readonly: the edit drawer's list. */
export type ChannelKeysVariant = 'manage' | 'balance' | 'readonly'

const PAGE_SIZE = 50
// The table only pages the keys here; the cells are StaticDataTable columns.
const NO_COLUMNS: ColumnDef<ChannelKey, unknown>[] = []

export function ChannelKeysContent(props: {
  channel: Channel
  variant: ChannelKeysVariant
  data: ChannelKeys
}) {
  const { t } = useTranslation()
  const { channel, variant, data } = props
  const manage = variant === 'manage'
  const canProbe = data.probe_enabled && data.probe_supported

  const models = useMemo(
    () => parseModelsString(channel.models),
    [channel.models]
  )
  const [model, setModel] = useState(() =>
    channel.test_model && models.includes(channel.test_model)
      ? channel.test_model
      : (models[0] ?? '')
  )
  const [statusFilter, setStatusFilter] = useState('all')
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set())
  const [pagination, setPagination] = useState({
    pageIndex: 0,
    pageSize: PAGE_SIZE,
  })

  const filtered = useMemo(
    () =>
      statusFilter === 'all'
        ? data.keys
        : data.keys.filter((key) => String(key.status) === statusFilter),
    [data.keys, statusFilter]
  )
  const allIndexes = useMemo(
    () => data.keys.map((key) => key.index),
    [data.keys]
  )
  const { progress, testResults, toggle, test, queryBalances } =
    useChannelKeyActions(channel, allIndexes)

  const ensurePageInRange = useCallback((pageCount: number) => {
    setPagination((previous) =>
      previous.pageIndex < pageCount
        ? previous
        : { ...previous, pageIndex: Math.max(pageCount - 1, 0) }
    )
  }, [])
  const { table } = useDataTable({
    data: filtered,
    columns: NO_COLUMNS,
    pagination,
    onPaginationChange: setPagination,
    autoResetPageIndex: false,
    ensurePageInRange,
    withFilteredRowModel: false,
    withSortedRowModel: false,
    withFacetedRowModel: false,
  })
  const pageKeys = table.getRowModel().rows.map((row) => row.original)

  // Keys removed from the channel since they were selected drop out here.
  const selectedIndexes = allIndexes.filter((index) => selected.has(index))
  const selectedInFilter = filtered.filter((key) => selected.has(key.index))
  const allSelected =
    filtered.length > 0 && selectedInFilter.length === filtered.length

  const select = (indexes: number[], checked: boolean) =>
    setSelected((previous) => {
      const next = new Set(previous)
      for (const index of indexes) {
        if (checked) next.add(index)
        else next.delete(index)
      }
      return next
    })

  // Keys already in that state would only spend new-api's rate limit.
  const toggleSelected = (enabled: boolean) => {
    const indexes = data.keys
      .filter(
        (key) =>
          selected.has(key.index) &&
          (key.status === CHANNEL_STATUS.ENABLED) !== enabled
      )
      .map((key) => key.index)
    if (indexes.length === 0) {
      toast.info(t('The selected keys are already in that state'))
      return
    }
    void toggle(enabled, indexes)
  }

  const busy = progress !== null
  let probeNotice: string | null = null
  if (!data.probe_enabled) {
    probeNotice = t(
      'Testing and querying the balance of each key is turned off by the super admin.'
    )
  } else if (!data.probe_supported) {
    probeNotice = t(
      'Testing and querying the balance of each key works only for OpenRouter channels whose API address is openrouter.ai.'
    )
  }

  return (
    <div className='space-y-3'>
      <ChannelKeysStats data={data} />
      {variant !== 'readonly' && probeNotice && (
        <Alert>
          <AlertDescription>{probeNotice}</AlertDescription>
        </Alert>
      )}
      {variant !== 'readonly' && (
        <ChannelKeysToolbar
          manage={manage}
          canProbe={canProbe}
          busy={busy}
          balancePending={
            progress?.action === 'balance' && progress.indexes.length > 1
          }
          statusFilter={statusFilter}
          onStatusFilterChange={(value) => {
            setStatusFilter(value)
            table.setPageIndex(0)
          }}
          models={models}
          model={model}
          onModelChange={setModel}
          onSelectPage={
            pageKeys.length > 0
              ? () =>
                  select(
                    pageKeys.map((key) => key.index),
                    true
                  )
              : undefined
          }
          onQueryAllBalances={() => void queryBalances()}
        />
      )}
      {manage && (
        <ChannelKeysBulkBar
          selectedCount={selectedIndexes.length}
          progress={progress}
          canProbe={canProbe}
          model={model}
          onTurnOn={() => toggleSelected(true)}
          onTurnOff={() => toggleSelected(false)}
          onTest={() => void test(selectedIndexes, model)}
          onQueryBalance={() => void queryBalances(selectedIndexes)}
          onClear={() => setSelected(new Set())}
        />
      )}
      <ChannelKeysTable
        keys={pageKeys}
        manage={
          manage
            ? {
                canProbe,
                model,
                testResults,
                progress,
                selected,
                allSelected,
                someSelected: selectedInFilter.length > 0 && !allSelected,
                onSelectAll: (checked) =>
                  select(
                    filtered.map((key) => key.index),
                    checked
                  ),
                onSelect: (index, checked) => select([index], checked),
                onToggle: (key) =>
                  void toggle(key.status !== CHANNEL_STATUS.ENABLED, [
                    key.index,
                  ]),
                onTest: (index) => void test([index], model),
                onQueryBalance: (index) => void queryBalances([index]),
              }
            : undefined
        }
      />
      {filtered.length > PAGE_SIZE && (
        <DataTablePagination table={table} compact />
      )}
    </div>
  )
}
