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
// Modified 2026-11-04 by new-api-sub: all channels of the scope loaded once, filtered/sorted/paged on the client; owner filter and deleted view; no tag mode, batch mode or drag reordering
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type {
  ColumnFiltersState,
  OnChangeFn,
  SortingState,
} from '@tanstack/react-table'
import { Eye, EyeOff, RefreshCw } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDebouncedColumnFilter,
  useDataTable,
  useDataTableViewMode,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { cn } from '@/lib/utils'

import { getChannels } from '../api'
import {
  DEFAULT_PAGE_SIZE,
  CHANNEL_STATUS,
  CHANNEL_STATUS_OPTIONS,
} from '../constants'
import { channelsQueryKeys, getChannelTypeLabel, parseGroupsList } from '../lib'
import type { Channel } from '../types'
import { ChannelCard } from './channel-card'
import { ChannelTypeLogo } from './channel-type-badge'
import { getChannelOwnerKey, useChannelsColumns } from './channels-columns'
import { useChannels } from './channels-provider'

const route = getRouteApi('/_authenticated/channels/')
const CHANNELS_COLUMN_VISIBILITY_STORAGE_KEY = 'channels:column-visibility'
const CHANNELS_COLUMN_SIZING_STORAGE_KEY = 'channels:column-sizing'
const CHANNELS_VIEW_MODE_STORAGE_KEY = 'channels:view-mode'
const CHANNELS_STATUS_FILTER_STORAGE_KEY = 'channel-status-filter'

function isDisabledChannelRow(channel: Channel) {
  return !channel.exists || channel.status !== CHANNEL_STATUS.ENABLED
}

export function ChannelsTable() {
  const { t } = useTranslation()
  const { mode, sorting, setSorting, sensitiveVisible, setSensitiveVisible } =
    useChannels()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const search = route.useSearch()
  const view =
    mode !== 'portal' && search.view === 'deleted' ? 'deleted' : 'active'

  // URL state management
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search,
    navigate: route.useNavigate(),
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      {
        columnId: 'status',
        searchKey: 'status',
        type: 'array',
        deserialize: (value) => {
          if (value !== undefined) return value
          const stored = localStorage.getItem(
            CHANNELS_STATUS_FILTER_STORAGE_KEY
          )
          return stored === 'enabled' || stored === 'disabled' ? [stored] : []
        },
      },
      { columnId: 'type', searchKey: 'type', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'array' },
      { columnId: 'models', searchKey: 'model', type: 'string' },
      ...(mode === 'portal'
        ? []
        : [
            {
              columnId: 'owner',
              searchKey: 'owner',
              type: 'array' as const,
            },
          ]),
    ],
  })

  const handleColumnFiltersChange: OnChangeFn<ColumnFiltersState> = (
    updater
  ) => {
    onColumnFiltersChange((previous) => {
      const next = typeof updater === 'function' ? updater(previous) : updater
      const status = next.find((f) => f.id === 'status')?.value as
        | string[]
        | undefined
      localStorage.setItem(
        CHANNELS_STATUS_FILTER_STORAGE_KEY,
        status?.[0] ?? 'all'
      )
      return next
    })
  }

  const {
    inputValue: modelFilterInput,
    onChange: onModelFilterInputChange,
    onCompositionStart: onModelFilterCompositionStart,
    onCompositionEnd: onModelFilterCompositionEnd,
    resetInput: resetModelFilterInput,
  } = useDebouncedColumnFilter({
    columnFilters,
    columnId: 'models',
    onColumnFiltersChange,
  })

  const handleSortingChange: OnChangeFn<SortingState> = (updater) => {
    setSorting(updater)
    if (pagination.pageIndex > 0) {
      onPaginationChange({ ...pagination, pageIndex: 0 })
    }
  }

  // Fetch channels data
  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: channelsQueryKeys.list({ view }),
    queryFn: () => getChannels(view),
    placeholderData: (previousData) => previousData,
  })
  const channels = useMemo(() => data ?? [], [data])

  const [viewMode, setViewMode] = useDataTableViewMode({
    storageKey: CHANNELS_VIEW_MODE_STORAGE_KEY,
    defaultMode: 'card',
  })

  const columns = useChannelsColumns({ showDeletedAt: view === 'deleted' })

  // React Table instance
  const { table } = useDataTable({
    data: channels,
    columns,
    sorting,
    initialColumnVisibility: {
      models: false,
      tag: false,
    },
    columnVisibilityStorageKey: CHANNELS_COLUMN_VISIBILITY_STORAGE_KEY,
    columnSizingStorageKey: isMobile
      ? false
      : CHANNELS_COLUMN_SIZING_STORAGE_KEY,
    columnFilters,
    pagination,
    globalFilter,
    globalFilterFn: (row, _columnId, filterValue) => {
      const keyword = String(filterValue).trim().toLowerCase()
      return (
        String(row.original.id).includes(keyword) ||
        row.original.name.toLowerCase().includes(keyword)
      )
    },
    // Refetches after a row action must not send the user back to page 1.
    autoResetPageIndex: false,
    onSortingChange: handleSortingChange,
    onColumnFiltersChange: handleColumnFiltersChange,
    onPaginationChange,
    onGlobalFilterChange,
    getRowId: (channel) => String(channel.id),
    enableColumnResizing: !isMobile,
    ensurePageInRange,
  })

  // Prepare filter options from existing channel types only.
  const typeFilterOptions = useMemo(() => {
    const counts = new Map<number, number>()
    for (const channel of channels) {
      counts.set(channel.type, (counts.get(channel.type) ?? 0) + 1)
    }
    const typeIds = Array.from(counts, ([type, count]) => ({ type, count }))
      .filter((item) => item.type > 0)
      .sort((a, b) => {
        const labelA = t(getChannelTypeLabel(a.type))
        const labelB = t(getChannelTypeLabel(b.type))
        return labelA.localeCompare(labelB)
      })

    return [
      {
        label: 'All Types',
        value: 'all',
        count: channels.length,
      },
      ...typeIds.map((item) => {
        return {
          label: getChannelTypeLabel(item.type),
          value: String(item.type),
          count: item.count,
          iconNode: <ChannelTypeLogo type={item.type} size={16} />,
        }
      }),
    ]
  }, [t, channels])

  const groupFilterOptions = useMemo(() => {
    const groups = new Set<string>()
    for (const channel of channels) {
      for (const group of parseGroupsList(channel.group)) groups.add(group)
    }
    return [
      { label: t('All Groups'), value: 'all' },
      ...[...groups]
        .sort((a, b) => a.localeCompare(b))
        .map((group) => ({
          label: sensitiveVisible ? group : '••••',
          value: group,
        })),
    ]
  }, [t, channels, sensitiveVisible])

  const ownerFilterOptions = useMemo(() => {
    const owners = new Map<string, { label: string; count: number }>()
    for (const { owner } of channels) {
      if (!owner) continue
      const key = getChannelOwnerKey(owner)
      const label = owner.user_id ? owner.username : owner.admin_username
      const seen = owners.get(key)
      owners.set(key, {
        label: seen?.label ?? label ?? key,
        count: (seen?.count ?? 0) + 1,
      })
    }
    return Array.from(owners, ([value, option]) => ({
      value,
      label: sensitiveVisible ? option.label : '••••',
      count: option.count,
    })).sort((a, b) => a.label.localeCompare(b.label))
  }, [channels, sensitiveVisible])

  return (
    <div
      role='region'
      aria-label={t('Channels')}
      className='relative h-full min-h-0'
    >
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No Channels Found')}
        emptyDescription={
          view === 'deleted'
            ? t('No deleted channels.')
            : t(
                'No channels available. Create your first channel to get started.'
              )
        }
        skeletonKeyPrefix='channel-skeleton'
        enableCardView
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        renderCard={(row, { isSelected }) => (
          <ChannelCard row={row} isSelected={isSelected} />
        )}
        cardGridClassName='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-3'
        applyHeaderSize
        toolbarProps={{
          collapsibleOnMobile: true,
          searchPlaceholder: t('Filter by name or ID...'),
          searchDebounceMs: 500,
          onReset: () => {
            resetModelFilterInput()
          },
          additionalSearch: (
            <Input
              placeholder={t('Filter by model...')}
              value={modelFilterInput}
              onChange={onModelFilterInputChange}
              onCompositionStart={onModelFilterCompositionStart}
              onCompositionEnd={onModelFilterCompositionEnd}
              className='w-full sm:w-[150px] lg:w-[180px]'
            />
          ),
          // The memoized filter reads its selection from a stable column; only a
          // fresh options array re-renders it after a client-side filter change.
          filters: [
            ...(mode === 'portal'
              ? []
              : [
                  {
                    columnId: 'owner',
                    title: t('Channel Owner'),
                    options: [...ownerFilterOptions],
                    singleSelect: true,
                  },
                ]),
            {
              columnId: 'status',
              title: t('Status'),
              options: [...CHANNEL_STATUS_OPTIONS],
              singleSelect: true,
            },
            {
              columnId: 'type',
              title: t('Type'),
              options: [...typeFilterOptions],
              singleSelect: true,
            },
            {
              columnId: 'group',
              title: t('Group'),
              options: [...groupFilterOptions],
              singleSelect: true,
            },
          ],
          preActions: (
            <>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant='ghost'
                      size='icon'
                      onClick={() => setSensitiveVisible(!sensitiveVisible)}
                      aria-label={sensitiveVisible ? t('Hide') : t('Show')}
                      className='text-muted-foreground hover:text-foreground size-8'
                    />
                  }
                >
                  {sensitiveVisible ? (
                    <Eye aria-hidden />
                  ) : (
                    <EyeOff aria-hidden />
                  )}
                </TooltipTrigger>
                <TooltipContent>
                  {sensitiveVisible ? t('Hide') : t('Show')}
                </TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant='ghost'
                      size='icon'
                      onClick={() => void refetch()}
                      aria-label={t('Refresh')}
                      aria-busy={isFetching}
                      className='text-muted-foreground hover:text-foreground size-8'
                    />
                  }
                >
                  <RefreshCw
                    aria-hidden
                    className={cn(isFetching && 'animate-spin')}
                  />
                </TooltipTrigger>
                <TooltipContent>{t('Refresh')}</TooltipContent>
              </Tooltip>
            </>
          ),
        }}
        getRowClassName={(row, { isMobile }) => {
          if (!isDisabledChannelRow(row.original)) {
            return undefined
          }
          if (isMobile) {
            return DISABLED_ROW_MOBILE
          }
          return DISABLED_ROW_DESKTOP
        }}
      />
    </div>
  )
}
