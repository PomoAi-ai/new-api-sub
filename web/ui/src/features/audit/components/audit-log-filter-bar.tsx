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
// Modified 2026-11-04 by new-api-sub: this project's categories and actions by role; the admin app adds username, source and IP
import type { Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { Combobox } from '@/components/ui/combobox'
import { CompactDateTimeRangePicker } from '@/features/logs/components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '@/features/logs/components/logs-filter-toolbar'
import { useAuthStore } from '@/stores/auth-store'

import type { AuditFilters, AuditLog, AuditMode } from '../api'
import { AUDIT_ACTIONS, AUDIT_CATEGORIES, auditOptionsFor } from '../constants'

function AuditFilterSelect(props: {
  label: string
  value: string
  options: { value: string; label: string; disabled?: boolean }[]
  onChange: (value: string) => void
}) {
  return (
    <LogsFilterField>
      <Combobox
        options={props.options}
        value={props.value}
        onValueChange={(value) => {
          if (value !== null) props.onChange(value)
        }}
        aria-label={props.label}
        className='w-full'
      />
    </LogsFilterField>
  )
}

export function AuditLogFilterBar(props: {
  table: Table<AuditLog>
  mode: AuditMode
  filters: AuditFilters
  onChange: (patch: Partial<AuditFilters>) => void
  isFetching: boolean
  onSearch: () => void
  onReset: () => void
}) {
  const { t } = useTranslation()
  const isSuper = useAuthStore((state) => state.auth.user?.role === 'super')
  const isAdmin = props.mode === 'admin'
  const dateFilter = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={
          props.filters.start === undefined
            ? undefined
            : new Date(props.filters.start * 1000)
        }
        end={
          props.filters.end === undefined
            ? undefined
            : new Date(props.filters.end * 1000)
        }
        onChange={({ start, end }) =>
          props.onChange({
            start: start ? Math.floor(start.getTime() / 1000) : undefined,
            end: end ? Math.floor(end.getTime() / 1000) : undefined,
          })
        }
      />
    </LogsFilterField>
  )
  const mainFilters = (
    <>
      <AuditFilterSelect
        label={t('Result')}
        value={props.filters.success ?? 'all'}
        options={[
          { value: 'all', label: t('All results') },
          { value: 'true', label: t('Success') },
          { value: 'false', label: t('Failed') },
        ]}
        onChange={(value) =>
          props.onChange({ success: value === 'all' ? undefined : value })
        }
      />
      <AuditFilterSelect
        label={t('Category')}
        value={props.filters.category ?? 'all'}
        options={[
          { value: 'all', label: t('All categories') },
          ...auditOptionsFor(AUDIT_CATEGORIES, props.mode, isSuper).map(
            (item) => ({ value: item.value, label: t(item.label) })
          ),
        ]}
        onChange={(value) =>
          props.onChange({ category: value === 'all' ? undefined : value })
        }
      />
    </>
  )
  const advancedFilters = (
    <>
      <AuditFilterSelect
        label={t('Action')}
        value={props.filters.action ?? 'all'}
        options={[
          { value: 'all', label: t('All actions') },
          ...auditOptionsFor(AUDIT_ACTIONS, props.mode, isSuper).map(
            (item) => ({ value: item.value, label: t(item.label) })
          ),
        ]}
        onChange={(value) =>
          props.onChange({ action: value === 'all' ? undefined : value })
        }
      />
      {isAdmin && (
        <>
          <LogsFilterField>
            <LogsFilterInput
              aria-label={t('Username')}
              placeholder={t('Username')}
              value={props.filters.actor_name ?? ''}
              onChange={(event) =>
                props.onChange({ actor_name: event.target.value || undefined })
              }
            />
          </LogsFilterField>
          <AuditFilterSelect
            label={t('Source')}
            value={props.filters.actor_type ?? 'all'}
            options={[
              { value: 'all', label: t('All sources') },
              { value: 'portal', label: t('Portal') },
              { value: 'admin', label: t('Admin') },
            ]}
            onChange={(value) =>
              props.onChange({
                actor_type:
                  value === 'portal' || value === 'admin' ? value : undefined,
              })
            }
          />
          <LogsFilterField>
            <LogsFilterInput
              aria-label={t('IP Address')}
              placeholder={t('IP Address')}
              value={props.filters.ip ?? ''}
              onChange={(event) =>
                props.onChange({ ip: event.target.value.trim() || undefined })
              }
            />
          </LogsFilterField>
        </>
      )}
    </>
  )
  const advancedCount = [
    props.filters.action,
    props.filters.actor_name,
    props.filters.actor_type,
    props.filters.ip,
  ].filter(Boolean).length
  const filterCount =
    advancedCount +
    [props.filters.success, props.filters.category].filter(Boolean).length
  const hasFilters =
    filterCount > 0 ||
    props.filters.start !== undefined ||
    props.filters.end !== undefined
  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          {dateFilter}
          {mainFilters}
        </>
      }
      advancedFilters={advancedFilters}
      mobilePinnedFilters={dateFilter}
      mobileFilters={
        <>
          {mainFilters}
          {advancedFilters}
        </>
      }
      mobileFilterCount={filterCount}
      advancedFilterCount={advancedCount}
      hasActiveFilters={hasFilters}
      hasAdvancedActiveFilters={advancedCount > 0}
      searchLoading={props.isFetching}
      onSearch={props.onSearch}
      onReset={props.onReset}
    />
  )
}
