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
// Modified 2026-11-08 by new-api-sub: only the row actions of this project, against our /api/channels endpoints (2xx is success, failures throw); retry and cancel of a push to a pool
import type { QueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { formatCurrencyFromUSD } from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerMessage } from '@/lib/server-error-message'

import {
  cancelChannelPlacement,
  deleteChannel,
  purgeChannel,
  retryChannelPlacement,
  restoreChannel,
  setChannelStatus,
  testChannel,
  updateChannelBalance,
} from '../api'
import { CHANNEL_STATUS, ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import type { ChannelTestResponse } from '../types'

// ============================================================================
// Query Keys
// ============================================================================

export const channelsQueryKeys = {
  all: ['channels'] as const,
  lists: () => [...channelsQueryKeys.all, 'list'] as const,
  list: (params: Record<string, unknown>) =>
    [...channelsQueryKeys.lists(), params] as const,
  types: () => [...channelsQueryKeys.all, 'types'] as const,
  ownerOptions: () => [...channelsQueryKeys.all, 'owner-options'] as const,
  keys: (id: number) => [...channelsQueryKeys.all, 'keys', id] as const,
  pools: () => [...channelsQueryKeys.all, 'pools'] as const,
  defaultBaseURLs: () =>
    [...channelsQueryKeys.all, 'default-base-urls'] as const,
}

function getChannelTestResponseTime(
  response: ChannelTestResponse
): number | undefined {
  if (
    typeof response.time === 'number' &&
    Number.isFinite(response.time) &&
    response.time > 0
  ) {
    return Math.round(response.time * 1000)
  }

  return undefined
}

function formatChannelTestDuration(responseTime?: number): string | undefined {
  if (responseTime === undefined) return undefined

  if (responseTime >= 1000) {
    return `${(responseTime / 1000).toFixed(2)} s`
  }

  return `${Math.max(1, Math.round(responseTime))} ms`
}

function getChannelTestLabel(options?: {
  channelName?: string
  testModel?: string
}): string {
  const channelName = options?.channelName?.trim()
  const testModel = options?.testModel?.trim()

  if (channelName && testModel) {
    return i18next.t('Channel {{name}} model {{model}}', {
      name: channelName,
      model: testModel,
    })
  }

  if (channelName) {
    return i18next.t('Channel {{name}}', { name: channelName })
  }

  if (testModel) {
    return i18next.t('Model {{model}}', { model: testModel })
  }

  return i18next.t('Channel')
}

// ============================================================================
// Single Channel Actions
// ============================================================================

/**
 * Toggle channel status (enable/disable)
 */
export async function handleToggleChannelStatus(
  id: number,
  currentStatus: number,
  queryClient: QueryClient
): Promise<void> {
  const enabled = currentStatus === CHANNEL_STATUS.ENABLED
  try {
    await setChannelStatus(
      id,
      enabled ? CHANNEL_STATUS.MANUAL_DISABLED : CHANNEL_STATUS.ENABLED
    )
    toast.success(
      i18next.t(enabled ? SUCCESS_MESSAGES.DISABLED : SUCCESS_MESSAGES.ENABLED)
    )
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  } catch (error) {
    handleServerError(error, i18next.t(ERROR_MESSAGES.UPDATE_FAILED))
  }
}

/**
 * Soft-delete a channel
 */
export async function handleDeleteChannel(
  id: number,
  queryClient: QueryClient
): Promise<void> {
  try {
    await deleteChannel(id)
    toast.success(i18next.t(SUCCESS_MESSAGES.DELETED))
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  } catch (error) {
    handleServerError(error, i18next.t(ERROR_MESSAGES.DELETE_FAILED))
  }
}

/**
 * Restore a soft-deleted channel; it stays disabled until enabled by hand
 */
export async function handleRestoreChannel(
  id: number,
  queryClient: QueryClient
): Promise<void> {
  try {
    await restoreChannel(id)
    toast.success(
      i18next.t('Channel restored. It stays disabled until you enable it.')
    )
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  } catch (error) {
    handleServerError(error, i18next.t('Failed to restore channel'))
  }
}

/**
 * Queue failed pushes again, one pool after another; stops at the first error
 */
export async function handleRetryPlacements(
  id: number,
  poolIds: number[],
  queryClient: QueryClient
): Promise<void> {
  try {
    for (const poolId of poolIds) await retryChannelPlacement(id, poolId)
    toast.success(i18next.t('Push queued again'))
  } catch (error) {
    handleServerError(error, i18next.t('Failed to retry the push'))
  } finally {
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  }
}

/**
 * Drop the push to one pool that has not created a channel there
 */
export async function handleCancelPlacement(
  id: number,
  poolId: number,
  queryClient: QueryClient
): Promise<void> {
  try {
    await cancelChannelPlacement(id, poolId)
    toast.success(i18next.t('Push cancelled'))
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  } catch (error) {
    handleServerError(error, i18next.t('Failed to cancel the push'))
  }
}

/**
 * Drop the local record of an orphan channel
 */
export async function handlePurgeChannel(
  id: number,
  queryClient: QueryClient
): Promise<void> {
  try {
    await purgeChannel(id)
    toast.success(i18next.t('Orphan channel cleaned up'))
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  } catch (error) {
    handleServerError(error, i18next.t(ERROR_MESSAGES.DELETE_FAILED))
  }
}

/**
 * Refresh the upstream balance; new-api's refusal (unsupported type, multi-key
 * channel) reaches the user as the server's message. Resolves with the upstream
 * JSON when new-api could not read a balance from it.
 */
export async function handleUpdateChannelBalance(
  id: number,
  queryClient: QueryClient
): Promise<string | undefined> {
  try {
    const { balance, raw_response: raw } = await updateChannelBalance(id)
    if (balance === undefined) return raw
    toast.success(
      i18next.t('Balance updated: {{balance}}', {
        balance: formatCurrencyFromUSD(balance, {
          digitsLarge: 2,
          digitsSmall: 4,
          abbreviate: false,
        }),
      })
    )
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  } catch (error) {
    handleServerError(error, i18next.t('Failed to update balance'))
  }
}

/**
 * Test channel connectivity
 */
export async function handleTestChannel(
  id: number,
  options?: {
    channelName?: string
    testModel?: string
    endpointType?: string
    stream?: boolean
    silent?: boolean
  },
  onTestComplete?: (
    success: boolean,
    responseTime?: number,
    error?: string
  ) => void
): Promise<void> {
  const payload = {
    ...(options?.testModel ? { model: options.testModel } : {}),
    ...(options?.endpointType ? { endpoint_type: options.endpointType } : {}),
    ...(options?.stream ? { stream: true } : {}),
  }

  try {
    const response = await testChannel(id, payload)
    const responseTime = getChannelTestResponseTime(response)
    const duration = formatChannelTestDuration(responseTime)
    const target = getChannelTestLabel(options)
    if (response.success) {
      if (!options?.silent) {
        toast.success(
          i18next.t('{{target}} test succeeded', { target }),
          duration
            ? {
                description: i18next.t('Response time: {{duration}}', {
                  duration,
                }),
              }
            : undefined
        )
      }
      onTestComplete?.(true, responseTime)
    } else {
      const errorMsg =
        getServerMessage(response) || i18next.t(ERROR_MESSAGES.TEST_FAILED)
      if (!options?.silent) {
        handleServerError(response, undefined, {
          title: i18next.t('{{target}} test failed', { target }),
          description: errorMsg,
        })
      }
      onTestComplete?.(false, responseTime, errorMsg)
    }
  } catch (_error: unknown) {
    const err = _error as { response?: { data?: unknown } }
    const errorMsg =
      getServerMessage(err?.response?.data) ||
      i18next.t(ERROR_MESSAGES.TEST_FAILED)
    const target = getChannelTestLabel(options)
    if (!options?.silent) {
      handleServerError(_error, undefined, {
        title: i18next.t('{{target}} test failed', { target }),
        description: errorMsg,
      })
    }
    onTestComplete?.(false, undefined, errorMsg)
  }
}
