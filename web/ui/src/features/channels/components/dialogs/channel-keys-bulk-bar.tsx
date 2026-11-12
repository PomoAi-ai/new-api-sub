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
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { KeyActionProgress } from './use-channel-key-actions'

/** Shown while keys are selected or a batch is running. */
export function ChannelKeysBulkBar(props: {
  selectedCount: number
  progress: KeyActionProgress | null
  canProbe: boolean
  model: string
  onTurnOn: () => void
  onTurnOff: () => void
  onTest: () => void
  onQueryBalance: () => void
  onClear: () => void
}) {
  const { t } = useTranslation()
  const { progress, selectedCount } = props
  const running = progress !== null && progress.indexes.length > 1
  if (selectedCount === 0 && !running) return null
  const busy = progress !== null
  return (
    <div className='bg-muted/50 flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm'>
      {selectedCount > 0 && (
        <>
          <span className='font-medium'>
            {t('{{n}} selected', { n: selectedCount })}
          </span>
          <Button
            size='sm'
            variant='outline'
            onClick={props.onTurnOn}
            disabled={busy}
          >
            {t('Turn On')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={props.onTurnOff}
            disabled={busy}
          >
            {t('Turn Off')}
          </Button>
          {props.canProbe && (
            <>
              <Button
                size='sm'
                variant='outline'
                onClick={props.onTest}
                disabled={busy || !props.model}
              >
                {t('Test')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                onClick={props.onQueryBalance}
                disabled={busy}
              >
                {t('Query Balance')}
              </Button>
            </>
          )}
          <Button size='sm' variant='ghost' onClick={props.onClear}>
            {t('Clear selection')}
          </Button>
        </>
      )}
      {running && (
        <span
          className='text-muted-foreground ml-auto flex items-center gap-1.5 tabular-nums'
          aria-live='polite'
        >
          <Loader2 className='size-4 animate-spin' />
          {t('Completed {{done}} / {{total}}', {
            done: progress.done,
            total: progress.indexes.length,
          })}
        </span>
      )}
    </div>
  )
}
