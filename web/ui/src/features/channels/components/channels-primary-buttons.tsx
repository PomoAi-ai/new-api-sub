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
// Modified 2026-11-04 by new-api-sub: sort control and create button; the batch/tag toggle pill now switches to the deleted view (admins only); no bulk actions menu
import { getRouteApi } from '@tanstack/react-router'
import { Archive, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import type { ChannelSortBy } from '../types'
import { ChannelSortControl } from './channel-sort-control'
import { useChannels } from './channels-provider'

const route = getRouteApi('/_authenticated/channels/')

export function ChannelsPrimaryButtons() {
  const { t } = useTranslation()
  const navigate = route.useNavigate()
  const { view } = route.useSearch()
  const { mode, setOpen, setCurrentRow, sorting, setSorting } = useChannels()

  return (
    <div className='flex flex-wrap items-center gap-2'>
      {mode !== 'portal' && (
        <div className='bg-muted/40 flex h-8 shrink-0 items-center gap-2.5 rounded-lg border px-2.5'>
          <div className='text-muted-foreground has-data-checked:text-foreground flex items-center gap-2'>
            <Label
              htmlFor='channel-deleted-view'
              className='cursor-pointer gap-1.5 text-xs whitespace-nowrap'
            >
              <Archive className='size-3.5' aria-hidden />
              <span className='max-sm:sr-only'>{t('Deleted Channels')}</span>
            </Label>
            <Switch
              id='channel-deleted-view'
              size='sm'
              checked={view === 'deleted'}
              onCheckedChange={(checked) => {
                void navigate({
                  search: (previous) => ({
                    ...previous,
                    view: checked ? 'deleted' : undefined,
                    page: 1,
                  }),
                  replace: true,
                })
              }}
            />
          </div>
        </div>
      )}

      <ChannelSortControl
        field={(sorting[0]?.id ?? 'priority') as ChannelSortBy}
        desc={sorting[0]?.desc ?? true}
        onChange={(field, desc) => {
          setSorting([{ id: field, desc }])
          void navigate({
            search: (previous) => ({ ...previous, page: 1 }),
            replace: true,
          })
        }}
      />

      {/* Create Channel */}
      <Button
        onClick={() => {
          setCurrentRow(null)
          setOpen('create-channel')
        }}
      >
        <Plus className='h-4 w-4' />
        <span className='max-sm:hidden'>{t('Create Channel')}</span>
        <span className='sm:hidden'>{t('Create')}</span>
      </Button>
    </div>
  )
}
