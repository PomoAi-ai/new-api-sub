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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  queryChannelKeyBalances,
  setChannelKeysEnabled,
  testChannelKeys,
} from '../../api'
import { channelsQueryKeys } from '../../lib'
import type { Channel, ChannelKeyTestResult } from '../../types'

// Each batch is one request; new-api rate-limits the switches per IP.
const TOGGLE_BATCH = 50
const TEST_BATCH = 32
const BALANCE_BATCH = 100

export type KeyAction = 'enable' | 'disable' | 'test' | 'balance'

export type KeyActionProgress = {
  action: KeyAction
  indexes: number[]
  done: number
}

/** result stays null until the key has been tested: it shows as not finished. */
export type KeyTestOutcome = {
  model: string
  result: ChannelKeyTestResult | null
}

/** Batches run one after another; a closed panel sends no further batch. */
export function useChannelKeyActions(channel: Channel, allIndexes: number[]) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const queryKey = channelsQueryKeys.keys(channel.id)
  const [progress, setProgress] = useState<KeyActionProgress | null>(null)
  const [testResults, setTestResults] = useState<
    ReadonlyMap<number, KeyTestOutcome>
  >(new Map())

  const closed = useRef(false)
  useEffect(() => {
    closed.current = false
    return () => {
      closed.current = true
    }
  }, [])

  const run = async (
    action: KeyAction,
    indexes: number[],
    batchSize: number,
    runBatch: (batch: number[]) => Promise<{ done: number; error?: string }>,
    onFinished: () => void
  ) => {
    let done = 0
    const failure = (reason: string) =>
      t('Completed {{done}} of {{total}}: {{reason}}', {
        done,
        total: indexes.length,
        reason,
      })
    setProgress({ action, indexes, done })
    try {
      for (let start = 0; start < indexes.length; start += batchSize) {
        if (closed.current) return
        const batch = indexes.slice(start, start + batchSize)
        const { done: finished, error } = await runBatch(batch)
        done += finished
        setProgress({ action, indexes, done })
        if (error) {
          toast.error(failure(error))
          return
        }
      }
      onFinished()
    } catch (error: unknown) {
      // An expired session has already been reported; this skips it.
      handleServerError(error, undefined, {
        title: failure(getServerErrorMessage(error, t('Operation failed'))),
      })
    } finally {
      setProgress(null)
      void queryClient.invalidateQueries({ queryKey })
      void queryClient.invalidateQueries({
        queryKey: channelsQueryKeys.lists(),
      })
    }
  }

  const toggle = (enabled: boolean, indexes: number[]) =>
    run(
      enabled ? 'enable' : 'disable',
      indexes,
      TOGGLE_BATCH,
      async (batch) => {
        const { done, error } = await setChannelKeysEnabled(
          channel.id,
          batch,
          enabled
        )
        void queryClient.invalidateQueries({ queryKey })
        return { done: done.length, error }
      },
      () =>
        toast.success(
          t(enabled ? '{{n}} key(s) turned on' : '{{n}} key(s) turned off', {
            n: indexes.length,
          })
        )
    )

  const test = (indexes: number[], model: string) => {
    let passed = 0
    let failed = 0
    setTestResults((previous) => {
      const next = new Map(previous)
      for (const index of indexes) next.set(index, { model, result: null })
      return next
    })
    return run(
      'test',
      indexes,
      TEST_BATCH,
      async (batch) => {
        const results = await testChannelKeys(channel.id, batch, model)
        setTestResults((previous) => {
          const next = new Map(previous)
          for (const result of results) {
            next.set(result.index, { model, result })
          }
          return next
        })
        for (const result of results) {
          if (result.ok) passed++
          else failed++
        }
        return { done: results.length }
      },
      () => {
        const unfinished = indexes.length - passed - failed
        const summary = t(
          'Test finished: {{passed}} passed, {{failed}} failed, {{unfinished}} not finished',
          { passed, failed, unfinished }
        )
        if (passed === indexes.length) toast.success(summary)
        else toast.warning(summary)
      }
    )
  }

  /** Without indexes every key is queried in one request. */
  const queryBalances = (indexes?: number[]) => {
    let failed = 0
    return run(
      'balance',
      indexes ?? allIndexes,
      indexes ? BALANCE_BATCH : allIndexes.length,
      async (batch) => {
        const data = await queryChannelKeyBalances(channel.id, indexes && batch)
        queryClient.setQueryData(queryKey, data)
        failed += data.keys.filter(
          (key) => batch.includes(key.index) && key.balance_error
        ).length
        return { done: batch.length }
      },
      () => {
        if (failed > 0) {
          toast.warning(
            t('{{n}} key(s) failed to query balance', { n: failed })
          )
        } else {
          toast.success(t('Balance updated successfully'))
        }
      }
    )
  }

  return { progress, testResults, toggle, test, queryBalances }
}
