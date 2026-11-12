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
// Modified 2026-11-04 by new-api-sub: admins loaded once and paginated/filtered client-side (from users/components/users-table.tsx)
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'

import { ADMIN_STATUS, adminsQueryKey, getAdmins } from '../api'
import { useAdminsColumns } from './admins-columns'

const route = getRouteApi('/_authenticated/admins/')

export function AdminsTable() {
  const { t } = useTranslation()
  const columns = useAdminsColumns()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'role', searchKey: 'role', type: 'array' },
    ],
  })

  const { data, isLoading, isFetching } = useQuery({
    queryKey: adminsQueryKey,
    queryFn: getAdmins,
  })

  const { table } = useDataTable({
    data: data ?? [],
    columns,
    columnFilters,
    globalFilter,
    pagination,
    globalFilterFn: (row, _columnId, filterValue) =>
      row.original.username
        .toLowerCase()
        .includes(String(filterValue).toLowerCase()),
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    ensurePageInRange,
  })

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Admins Found')}
      emptyDescription={t(
        'No admins available. Try adjusting your search or filters.'
      )}
      skeletonKeyPrefix='admins-skeleton'
      applyHeaderSize
      toolbarProps={{
        searchPlaceholder: t('Filter by username...'),
        filters: [
          {
            columnId: 'status',
            title: t('Status'),
            options: [
              { label: t('Enabled'), value: String(ADMIN_STATUS.ENABLED) },
              { label: t('Disabled'), value: String(ADMIN_STATUS.DISABLED) },
            ],
            singleSelect: true,
          },
          {
            columnId: 'role',
            title: t('Role'),
            options: [
              { label: t('Super Admin'), value: 'super' },
              { label: t('Admin'), value: 'admin' },
            ],
            singleSelect: true,
          },
        ],
      }}
      getRowClassName={(row, { isMobile }) => {
        if (row.original.status !== ADMIN_STATUS.DISABLED) return undefined
        return isMobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
      }}
    />
  )
}
