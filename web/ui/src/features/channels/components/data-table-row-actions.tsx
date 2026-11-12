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
// Modified 2026-11-08 by new-api-sub: edit (name and models), test, enable/disable, balance refresh, the keys of a multi-key channel (by number only) and soft delete; restore, failed push retry and orphan cleanup for admins; no copy, fetch models or Ollama
import { useQueryClient } from '@tanstack/react-query'
import type { Row } from '@tanstack/react-table'
import {
  ArchiveRestore,
  Eraser,
  MoreHorizontal,
  Pencil,
  PlugZap,
  Gauge,
  DollarSign,
  KeyRound,
  Power,
  PowerOff,
  RotateCcw,
  Trash2,
  Loader2,
} from 'lucide-react'
import { useContext, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import {
  channelsQueryKeys,
  failedPoolIds,
  handleDeleteChannel,
  handlePurgeChannel,
  handleRestoreChannel,
  handleRetryPlacements,
  handleTestChannel,
  handleToggleChannelStatus,
  isChannelEnabled,
  isChannelPushing,
  isMultiKeyChannel,
} from '../lib'
import type { Channel } from '../types'
import { ChannelRowActionsLayoutContext } from './channel-row-actions-context'
import { useChannels } from './channels-provider'

interface DataTableRowActionsProps {
  row: Row<Channel>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const layout = useContext(ChannelRowActionsLayoutContext)
  const channel = row.original
  const { mode, setOpen, setCurrentRow } = useChannels()
  const queryClient = useQueryClient()
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [purgeConfirmOpen, setPurgeConfirmOpen] = useState(false)
  const [isTesting, setIsTesting] = useState(false)
  const [isTogglingStatus, setIsTogglingStatus] = useState(false)
  const [isRestoring, setIsRestoring] = useState(false)

  const isEnabled = isChannelEnabled(channel)
  const isDeleted = Boolean(channel.deleted_at)
  // Orphans and soft-deleted channels have nothing upstream to act on.
  const isLive = channel.exists && !isDeleted
  // While a push is on its way, renaming and enabling apply once it lands.
  const canChange = isLive || (!isDeleted && isChannelPushing(channel))
  const canPurge =
    mode !== 'portal' && !channel.exists && !isChannelPushing(channel)
  const retryPools = mode === 'portal' ? [] : failedPoolIds(channel)

  const handleEdit = () => {
    setCurrentRow(channel)
    setOpen('edit-channel')
  }

  const handleTest = () => {
    setCurrentRow(channel)
    setOpen('test-channel')
  }

  const handleQueryBalance = () => {
    setCurrentRow(channel)
    setOpen('balance-query')
  }

  const handleManageKeys = () => {
    setCurrentRow(channel)
    setOpen('manage-keys')
  }

  const handleDirectTest = async (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    setIsTesting(true)
    try {
      await handleTestChannel(channel.id, { channelName: channel.name }, () => {
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      })
    } finally {
      setIsTesting(false)
    }
  }

  const handleToggleStatus = async (
    e?: React.MouseEvent<HTMLButtonElement>
  ) => {
    e?.stopPropagation()
    setIsTogglingStatus(true)
    try {
      await handleToggleChannelStatus(channel.id, channel.status, queryClient)
    } finally {
      setIsTogglingStatus(false)
    }
  }

  const handleRestore = async (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    setIsRestoring(true)
    try {
      await handleRestoreChannel(channel.id, queryClient)
    } finally {
      setIsRestoring(false)
    }
  }

  let statusIcon = <Power className='size-4' />
  if (isTogglingStatus) {
    statusIcon = <Loader2 className='size-4 animate-spin' />
  } else if (isEnabled) {
    statusIcon = <PowerOff className='size-4' />
  }

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      {isLive && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={handleDirectTest}
                disabled={isTesting}
                aria-label={t('Test Connection')}
              />
            }
          >
            {isTesting ? (
              <Loader2 className='size-4 animate-spin' />
            ) : (
              <Gauge className='size-4' />
            )}
          </TooltipTrigger>
          <TooltipContent>{t('Test Connection')}</TooltipContent>
        </Tooltip>
      )}

      {isLive && layout === 'card' && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={(e) => {
                  e.stopPropagation()
                  handleTest()
                }}
                aria-label={t('Test Channel Connection')}
              />
            }
          >
            <PlugZap className='size-4' />
          </TooltipTrigger>
          <TooltipContent>{t('Test Channel Connection')}</TooltipContent>
        </Tooltip>
      )}

      {canChange && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={handleToggleStatus}
                disabled={isTogglingStatus}
                aria-label={isEnabled ? t('Disable') : t('Enable')}
                className={
                  isEnabled
                    ? 'text-destructive hover:text-destructive'
                    : 'text-success hover:text-success'
                }
              />
            }
          >
            {statusIcon}
          </TooltipTrigger>
          <TooltipContent>
            {isEnabled ? t('Disable') : t('Enable')}
          </TooltipContent>
        </Tooltip>
      )}

      {isDeleted && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={handleRestore}
                disabled={isRestoring}
                aria-label={t('Restore')}
              />
            }
          >
            {isRestoring ? (
              <Loader2 className='size-4 animate-spin' />
            ) : (
              <ArchiveRestore className='size-4' />
            )}
          </TooltipTrigger>
          <TooltipContent>{t('Restore')}</TooltipContent>
        </Tooltip>
      )}

      {(!isDeleted || canPurge) && (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant='ghost'
                className='data-popup-open:bg-muted flex h-8 w-8 p-0'
              />
            }
          >
            <MoreHorizontal className='h-4 w-4' />
            <span className='sr-only'>{t('Open menu')}</span>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end' className='w-48'>
            {canChange && (
              <>
                {/* Edit: name and models only */}
                <DropdownMenuItem onClick={handleEdit}>
                  {t('Edit')}
                  <DropdownMenuShortcut>
                    <Pencil size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
                {!isLive && <DropdownMenuSeparator />}
              </>
            )}

            {retryPools.length > 0 && (
              <>
                <DropdownMenuItem
                  onClick={() =>
                    void handleRetryPlacements(
                      channel.id,
                      retryPools,
                      queryClient
                    )
                  }
                >
                  {t('Retry failed pushes')}
                  <DropdownMenuShortcut>
                    <RotateCcw size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
                <DropdownMenuSeparator />
              </>
            )}

            {isLive && (
              <>
                {/* Test Connection */}
                <DropdownMenuItem onClick={handleTest}>
                  {t('Test Connection')}
                  <DropdownMenuShortcut>
                    <PlugZap size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>

                {/* Query Balance */}
                <DropdownMenuItem onClick={handleQueryBalance}>
                  {t('Query Balance')}
                  <DropdownMenuShortcut>
                    <DollarSign size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>

                {isMultiKeyChannel(channel) && (
                  <DropdownMenuItem onClick={handleManageKeys}>
                    {t('Manage Keys')}
                    <DropdownMenuShortcut>
                      <KeyRound size={16} />
                    </DropdownMenuShortcut>
                  </DropdownMenuItem>
                )}

                <DropdownMenuSeparator />
              </>
            )}

            {/* Delete */}
            {!isDeleted && (
              <DropdownMenuItem
                onClick={() => setDeleteConfirmOpen(true)}
                className='text-destructive focus:text-destructive'
              >
                {t('Delete')}
                <DropdownMenuShortcut>
                  <Trash2 size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
            )}

            {/* Clean up an orphan */}
            {canPurge && (
              <DropdownMenuItem
                onClick={() => setPurgeConfirmOpen(true)}
                className='text-destructive focus:text-destructive'
              >
                {t('Clean up')}
                <DropdownMenuShortcut>
                  <Eraser size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}

      <ConfirmDialog
        open={deleteConfirmOpen}
        onOpenChange={setDeleteConfirmOpen}
        title={t('Delete Channel')}
        desc={t(
          'Are you sure you want to delete channel "{{name}}"? It will only be hidden and disabled, and an administrator can restore it.',
          { name: channel.name }
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={() => {
          void handleDeleteChannel(channel.id, queryClient)
          setDeleteConfirmOpen(false)
        }}
      />

      <ConfirmDialog
        open={purgeConfirmOpen}
        onOpenChange={setPurgeConfirmOpen}
        title={t('Clean up orphan channel')}
        desc={t(
          'Channel "{{name}}" no longer exists upstream. Its local record will be removed permanently. This action cannot be undone.',
          { name: channel.name }
        )}
        confirmText={t('Clean up')}
        destructive
        handleConfirm={() => {
          void handlePurgeChannel(channel.id, queryClient)
          setPurgeConfirmOpen(false)
        }}
      />
    </div>
  )
}
