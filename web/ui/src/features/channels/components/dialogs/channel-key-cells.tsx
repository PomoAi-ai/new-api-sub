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
import { DollarSign, Loader2, PlugZap, Power, PowerOff } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { CHANNEL_STATUS, CHANNEL_STATUS_CONFIG } from '../../constants'
import type { ChannelKey } from '../../types'
import type { ChannelKeysTableManage } from './channel-keys-table'
import type { KeyAction, KeyTestOutcome } from './use-channel-key-actions'

export function KeyStatusBadge(props: { status: number }) {
  const { t } = useTranslation()
  const config =
    CHANNEL_STATUS_CONFIG[props.status as keyof typeof CHANNEL_STATUS_CONFIG] ??
    CHANNEL_STATUS_CONFIG[CHANNEL_STATUS.UNKNOWN]
  const label =
    props.status === CHANNEL_STATUS.MANUAL_DISABLED
      ? 'Manually Disabled'
      : config.label
  return (
    <StatusBadge
      label={t(label)}
      variant={config.variant}
      showDot
      copyable={false}
    />
  )
}

export function KeyTestCell(props: { outcome?: KeyTestOutcome }) {
  const { t } = useTranslation()
  if (!props.outcome) return '-'
  const { model, result } = props.outcome
  let badge = (
    <StatusBadge label={t('Not finished')} variant='neutral' copyable={false} />
  )
  if (result) {
    badge = (
      <StatusBadge
        label={result.ok ? t('Passed') : t('Failed')}
        variant={result.ok ? 'success' : 'danger'}
        copyable={false}
      />
    )
  }
  return (
    <div className='space-y-0.5 text-sm whitespace-normal'>
      <div className='flex flex-wrap items-center gap-x-2'>
        {badge}
        {result && (
          <span className='text-muted-foreground text-xs tabular-nums'>
            {result.latency_ms} ms
          </span>
        )}
      </div>
      <div className='text-muted-foreground truncate text-xs' title={model}>
        {model}
      </div>
      {result && !result.ok && result.message && (
        <div
          className='text-destructive line-clamp-2 text-xs break-all'
          title={result.message}
        >
          {result.message}
        </div>
      )}
    </div>
  )
}

function IconAction(props: {
  label: string
  onClick: () => void
  pending: boolean
  disabled: boolean
  icon: ReactNode
  className?: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant='ghost'
            size='icon-sm'
            onClick={props.onClick}
            disabled={props.disabled}
            aria-label={props.label}
            className={props.className}
          />
        }
      >
        {props.pending ? (
          <Loader2 className='size-4 animate-spin' />
        ) : (
          props.icon
        )}
      </TooltipTrigger>
      <TooltipContent>{props.label}</TooltipContent>
    </Tooltip>
  )
}

export function KeyRowActions(props: {
  channelKey: ChannelKey
  manage: ChannelKeysTableManage
}) {
  const { t } = useTranslation()
  const { channelKey: key, manage } = props
  const busy = manage.progress !== null
  const enabled = key.status === CHANNEL_STATUS.ENABLED
  const isPending = (...actions: KeyAction[]) => {
    const { progress } = manage
    return (
      progress?.indexes.length === 1 &&
      progress.indexes[0] === key.index &&
      actions.includes(progress.action)
    )
  }
  return (
    <div className='flex items-center justify-end gap-1'>
      <IconAction
        label={enabled ? t('Turn Off') : t('Turn On')}
        onClick={() => manage.onToggle(key)}
        pending={isPending('enable', 'disable')}
        disabled={busy}
        icon={
          enabled ? (
            <PowerOff className='size-4' />
          ) : (
            <Power className='size-4' />
          )
        }
        className={
          enabled
            ? 'text-destructive hover:text-destructive'
            : 'text-success hover:text-success'
        }
      />
      {manage.canProbe && (
        <>
          <IconAction
            label={t('Test')}
            onClick={() => manage.onTest(key.index)}
            pending={isPending('test')}
            disabled={busy || !manage.model}
            icon={<PlugZap className='size-4' />}
          />
          <IconAction
            label={t('Query Balance')}
            onClick={() => manage.onQueryBalance(key.index)}
            pending={isPending('balance')}
            disabled={busy}
            icon={<DollarSign className='size-4' />}
          />
        </>
      )}
    </div>
  )
}
