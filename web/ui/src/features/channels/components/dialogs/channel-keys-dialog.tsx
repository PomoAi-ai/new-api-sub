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
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'

import { getChannelKeys } from '../../api'
import { channelsQueryKeys } from '../../lib'
import type { Channel } from '../../types'
import {
  ChannelKeysContent,
  type ChannelKeysVariant,
} from './channel-keys-content'

/** The keys of a multi-key channel by number; the key itself is never fetched. */
export function ChannelKeysPanel(props: {
  channel: Channel
  variant: ChannelKeysVariant
}) {
  const { t } = useTranslation()
  const { channel, variant } = props
  const query = useQuery({
    queryKey: channelsQueryKeys.keys(channel.id),
    queryFn: () => getChannelKeys(channel.id),
  })

  // A failed refetch keeps the data: unmounting would lose the selection and a running batch.
  if (query.isError && !query.data) {
    return (
      <ErrorState
        title={t('Failed to load keys')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  if (!query.data) {
    return (
      <div className='flex items-center justify-center py-12'>
        <Loader2 className='text-muted-foreground size-8 animate-spin' />
      </div>
    )
  }
  return (
    <ChannelKeysContent channel={channel} variant={variant} data={query.data} />
  )
}

export function ChannelKeysDialog(props: {
  channel: Channel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  if (!props.channel) return null
  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Manage Keys')}
      description={
        <>
          <strong>{props.channel.name}</strong>{' '}
          {t(
            'A test makes one real call with the selected model, directly to OpenRouter, without going through new-api or into the usage logs.'
          )}
        </>
      }
      contentClassName='sm:max-w-6xl'
      contentHeight='auto'
    >
      <ChannelKeysPanel channel={props.channel} variant='manage' />
    </Dialog>
  )
}
