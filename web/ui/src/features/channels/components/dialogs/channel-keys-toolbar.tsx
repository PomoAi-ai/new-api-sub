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
import { Loader2, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { CHANNEL_STATUS } from '../../constants'
import { formatKeyBalance } from '../../lib'
import type { ChannelKeys } from '../../types'

export function ChannelKeysStats(props: { data: ChannelKeys }) {
  const { t } = useTranslation()
  const { data } = props
  const count = (status: number) =>
    data.keys.filter((key) => key.status === status).length
  const stats = [
    { label: t('Enabled'), value: count(CHANNEL_STATUS.ENABLED) },
    {
      label: t('Manually Disabled'),
      value: count(CHANNEL_STATUS.MANUAL_DISABLED),
    },
    { label: t('Auto Disabled'), value: count(CHANNEL_STATUS.AUTO_DISABLED) },
  ]
  return (
    <div className='flex flex-wrap items-baseline gap-x-5 gap-y-1 text-sm'>
      <span>
        <span className='text-muted-foreground'>{t('Total Balance')}: </span>
        <span className='font-mono font-semibold tabular-nums'>
          {data.total_balance == null
            ? '-'
            : formatKeyBalance(data.total_balance)}
        </span>
      </span>
      {stats.map((stat) => (
        <span key={stat.label}>
          <span className='text-muted-foreground'>{stat.label}: </span>
          <span className='font-semibold tabular-nums'>{stat.value}</span>
        </span>
      ))}
    </div>
  )
}

function KeysSelect(props: {
  items: { value: string; label: string }[]
  value: string
  onChange: (value: string) => void
  label: string
  disabled?: boolean
  className: string
}) {
  return (
    <Select
      items={props.items}
      value={props.value}
      onValueChange={(value) => {
        if (value !== null) props.onChange(value)
      }}
      disabled={props.disabled}
    >
      <SelectTrigger className={props.className} aria-label={props.label}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          {props.items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

/** Status filter and model for manage, the balance of every key for both. */
export function ChannelKeysToolbar(props: {
  manage: boolean
  canProbe: boolean
  busy: boolean
  balancePending: boolean
  statusFilter: string
  onStatusFilterChange: (value: string) => void
  models: string[]
  model: string
  onModelChange: (value: string) => void
  onSelectPage?: () => void
  onQueryAllBalances: () => void
}) {
  const { t } = useTranslation()
  const { manage, canProbe, busy } = props
  return (
    <div className='flex flex-wrap items-center gap-2'>
      {manage && (
        <KeysSelect
          label={t('Status')}
          className='w-32'
          items={[
            { value: 'all', label: t('All') },
            { value: String(CHANNEL_STATUS.ENABLED), label: t('Enabled') },
            {
              value: String(CHANNEL_STATUS.MANUAL_DISABLED),
              label: t('Manually Disabled'),
            },
            {
              value: String(CHANNEL_STATUS.AUTO_DISABLED),
              label: t('Auto Disabled'),
            },
          ]}
          value={props.statusFilter}
          onChange={props.onStatusFilterChange}
        />
      )}
      {manage && canProbe && (
        <KeysSelect
          label={t('Model')}
          className='w-64 max-w-full'
          items={props.models.map((name) => ({ value: name, label: name }))}
          value={props.model}
          onChange={props.onModelChange}
          disabled={busy}
        />
      )}
      {manage && (
        <Button
          size='sm'
          variant='outline'
          onClick={props.onSelectPage}
          disabled={!props.onSelectPage}
        >
          {t('Select this page')}
        </Button>
      )}
      {canProbe && (
        <Button
          size='sm'
          variant='outline'
          className='ml-auto'
          onClick={props.onQueryAllBalances}
          disabled={busy}
        >
          {props.balancePending ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <RefreshCw className='size-4' />
          )}
          {t('Query All Balances')}
        </Button>
      )}
    </div>
  )
}
