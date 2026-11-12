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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { Combobox } from '@/components/ui/combobox'

import { getChannelOptions, getPoolNames, getUserOptions } from '../api'
import type { LogsMode } from '../types'

/**
 * Channel multi-select of the dashboard's filters. In the admin app
 * the options narrow to the selected user's channels, since the backend
 * rejects channels outside that user's scope.
 */
export function ChannelFilter(props: {
  mode: LogsMode
  userId?: number
  value: number[]
  onChange: (ids: number[]) => void
  className?: string
}) {
  const { t } = useTranslation()
  const { data: channels = [] } = useQuery({
    queryKey: ['filter-channels', props.mode],
    queryFn: getChannelOptions,
  })
  const options = useMemo(
    () =>
      channels
        .filter(
          (c) => props.userId == null || c.owner?.user_id === props.userId
        )
        .map((c) => ({
          label: `${c.name} #${c.id}`,
          value: String(c.id),
          hint: c.owner?.username,
        })),
    [channels, props.userId]
  )

  return (
    <MultiSelect
      options={options}
      selected={props.value.map(String)}
      onChange={(values) => props.onChange(values.map(Number))}
      placeholder={t('All Channels')}
      emptyText={t('No channel found.')}
      renderSelectedSummary={
        props.value.length > 0
          ? (values) => t('{{count}} channels', { count: values.length })
          : undefined
      }
      className={props.className}
    />
  )
}

/** Pool picker of the log and stats pages; empty means all pools. */
export function PoolFilter(props: {
  mode: LogsMode
  value?: string
  onChange: (value: string | undefined) => void
  className?: string
}) {
  const { t } = useTranslation()
  const { data: pools = [] } = useQuery({
    queryKey: ['pool-names'],
    queryFn: getPoolNames,
  })
  const options = useMemo(
    () =>
      pools.map((p) => ({ label: p.name || `#${p.id}`, value: String(p.id) })),
    [pools]
  )

  return (
    <Combobox
      options={options}
      aria-label={t('Pool')}
      emptyText={t('No pool found.')}
      placeholder={t('All Pools')}
      className={props.className}
      value={props.value ?? ''}
      onValueChange={(value) => props.onChange(value || undefined)}
    />
  )
}

/** Admin-only portal user picker; empty means all users. */
export function UserFilter(props: {
  value?: number
  onChange: (id: number | undefined) => void
  className?: string
}) {
  const { t } = useTranslation()
  const { data: users = [] } = useQuery({
    queryKey: ['filter-users'],
    queryFn: getUserOptions,
  })
  const options = useMemo(
    () => users.map((u) => ({ label: u.username, value: String(u.id) })),
    [users]
  )

  return (
    <Combobox
      options={options}
      aria-label={t('User')}
      emptyText={t('No user found.')}
      placeholder={t('All Users')}
      className={props.className}
      value={props.value == null ? '' : String(props.value)}
      onValueChange={(value) =>
        props.onChange(value ? Number(value) : undefined)
      }
    />
  )
}
