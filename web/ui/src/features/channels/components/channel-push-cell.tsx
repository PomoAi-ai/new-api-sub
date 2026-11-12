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
import { useQueryClient } from '@tanstack/react-query'
import { RotateCcw, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'

import { handleCancelPlacement, handleRetryPlacements } from '../lib'
import type { Channel, ChannelPlacement, ChannelPushState } from '../types'
import { useChannels } from './channels-provider'

const PUSH_STATE: Record<
  ChannelPushState,
  { label: string; variant: StatusVariant }
> = {
  pending: { label: 'Pushing', variant: 'info' },
  partial: { label: 'Partly pushed', variant: 'warning' },
  failed: { label: 'Push failed', variant: 'danger' },
  done: { label: 'Push complete', variant: 'success' },
}

const PLACEMENT_STATE: Record<
  ChannelPlacement['state'],
  { label: string; variant: StatusVariant }
> = {
  pending: { label: 'Waiting', variant: 'neutral' },
  pushing: { label: 'Pushing', variant: 'info' },
  created: { label: 'Created in pool', variant: 'success' },
  failed: { label: 'Failed', variant: 'danger' },
}

function PlacementItem(props: {
  channel: Channel
  placement: ChannelPlacement
  onCancel: (poolId: number) => void
}) {
  const { placement } = props
  const { t } = useTranslation()
  const { mode } = useChannels()
  const queryClient = useQueryClient()
  const state = PLACEMENT_STATE[placement.state]
  const poolId = placement.pool_id
  return (
    <li className='space-y-1 py-2'>
      <div className='flex items-center justify-between gap-2'>
        <span className='min-w-0 truncate text-sm'>
          {placement.pool_name ?? `#${poolId}`}
          {placement.newapi_channel_id != null && (
            <span className='text-muted-foreground font-mono text-xs'>
              {' '}
              #{placement.newapi_channel_id}
            </span>
          )}
        </span>
        <div className='flex shrink-0 items-center gap-1 max-sm:gap-2'>
          <StatusBadge
            label={t(state.label)}
            variant={state.variant}
            size='sm'
            copyable={false}
          />
          {mode !== 'portal' &&
            placement.state === 'failed' &&
            poolId != null && (
              <Button
                variant='ghost'
                size='icon-sm'
                className='max-sm:size-10'
                aria-label={t('Retry push')}
                title={t('Retry push')}
                onClick={() =>
                  void handleRetryPlacements(
                    props.channel.id,
                    [poolId],
                    queryClient
                  )
                }
              >
                <RotateCcw className='size-4' />
              </Button>
            )}
          {mode === 'super' &&
            placement.state !== 'created' &&
            poolId != null && (
              <Button
                variant='ghost'
                size='icon-sm'
                className='text-destructive hover:text-destructive max-sm:size-10'
                aria-label={t('Cancel push')}
                title={t('Cancel push')}
                onClick={() => props.onCancel(poolId)}
              >
                <X className='size-4' />
              </Button>
            )}
        </div>
      </div>
      {mode !== 'portal' && placement.error && (
        <p className='text-destructive text-xs wrap-anywhere'>
          {placement.error}
        </p>
      )}
    </li>
  )
}

/**
 * Push state and how many pools have the channel, with each pool's name and
 * state. Admins also get its error and actions; the portal sees neither.
 */
export function ChannelPushCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [cancelling, setCancelling] = useState<number | null>(null)
  const placements = channel.placements ?? []
  const created = placements.filter((p) => p.state === 'created').length
  const state = PUSH_STATE[channel.push_state]
  const summary = t('{{created}}/{{total}} pools placed', {
    created,
    total: placements.length,
  })

  return (
    <>
      <Popover>
        <PopoverTrigger
          render={
            <button
              type='button'
              className='-ml-1.5 inline-flex items-center gap-1.5 rounded-md'
              aria-label={`${t(state.label)}, ${summary}`}
            />
          }
        >
          <StatusBadge
            label={t(state.label)}
            variant={state.variant}
            size='sm'
            copyable={false}
            className='cursor-pointer'
          />
          <span className='text-muted-foreground text-xs tabular-nums'>
            {created}/{placements.length}
          </span>
        </PopoverTrigger>
        <PopoverContent align='start' className='w-72'>
          <p className='text-sm font-medium'>{summary}</p>
          {placements.length > 0 && (
            <ul className='divide-y'>
              {placements.map((placement, index) => (
                <PlacementItem
                  key={placement.pool_id ?? index}
                  channel={channel}
                  placement={placement}
                  onCancel={setCancelling}
                />
              ))}
            </ul>
          )}
        </PopoverContent>
      </Popover>
      <ConfirmDialog
        open={cancelling !== null}
        onOpenChange={(open) => !open && setCancelling(null)}
        title={t('Cancel push')}
        desc={t(
          'The channel will not be created in this pool. This cannot be undone.'
        )}
        confirmText={t('Cancel push')}
        destructive
        handleConfirm={() => {
          if (cancelling !== null) {
            void handleCancelPlacement(channel.id, cancelling, queryClient)
          }
          setCancelling(null)
        }}
      />
    </>
  )
}
