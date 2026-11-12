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
// Modified 2026-11-04 by new-api-sub: test state and batch logic split out of channel-test-dialog.tsx; patches our plain list cache
import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import type { TestResult } from '../components/dialogs/channel-test-results'
import { channelsQueryKeys, handleTestChannel } from '../lib'
import type { Channel } from '../types'

type BatchProgress = {
  total: number
  completed: number
  success: number
  failed: number
}

type ChannelTestCachePatch = {
  responseTime: number
  testTime: number
}

type LatestChannelTestCachePatch = {
  patch: ChannelTestCachePatch
  completedAt: number
}

function createChannelTestCachePatch(
  responseTime?: number,
  completedAt = Date.now()
): ChannelTestCachePatch | undefined {
  if (typeof responseTime !== 'number' || !Number.isFinite(responseTime)) {
    return undefined
  }

  return {
    responseTime,
    testTime: Math.floor(completedAt / 1000),
  }
}

function getLatestChannelTestCachePatch(
  results: TestResult[]
): ChannelTestCachePatch | undefined {
  const latest = results.reduce<LatestChannelTestCachePatch | undefined>(
    (latestPatch, result) => {
      const completedAt = result.completedAt ?? 0
      const patch = createChannelTestCachePatch(
        result.responseTime,
        completedAt
      )
      if (!patch) return latestPatch
      if (!latestPatch || completedAt >= latestPatch.completedAt) {
        return { patch, completedAt }
      }
      return latestPatch
    },
    undefined
  )

  return latest?.patch
}

const BATCH_TEST_CONCURRENCY = 5
const BATCH_TEST_DELAY_MS = 100

function sleep(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms))
}

export function useChannelModelTests(options: {
  channel: Channel
  endpointType: string
  stream: boolean
  /** Called when a model's result changes, so stale failure details can close. */
  onResultChange: (model: string) => void
  onBatchEnd: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentRow = options.channel
  const currentChannelId = currentRow.id
  const endpointType = options.endpointType
  const effectiveStreamTest = options.stream
  const onResultChange = options.onResultChange
  const onBatchEnd = options.onBatchEnd
  const batchStopRequestedRef = useRef(false)
  const batchProgressToastIdRef = useRef<ReturnType<
    typeof toast.loading
  > | null>(null)
  const [testResults, setTestResults] = useState<Record<string, TestResult>>({})
  const [testingModels, setTestingModels] = useState<Set<string>>(
    () => new Set()
  )
  const [isBatchTesting, setIsBatchTesting] = useState(false)
  const [isBatchStopRequested, setIsBatchStopRequested] = useState(false)
  const [batchProgress, setBatchProgress] = useState<BatchProgress | null>(null)

  const dismissBatchProgressToast = useCallback(() => {
    if (batchProgressToastIdRef.current === null) return

    toast.dismiss(batchProgressToastIdRef.current)
    batchProgressToastIdRef.current = null
  }, [])

  useEffect(() => {
    if (!batchProgress) {
      dismissBatchProgressToast()
      return
    }

    const title = isBatchStopRequested
      ? t('Stopping batch test...')
      : t('Batch testing models...')
    const completedText = t('{{completed}}/{{total}} completed', {
      completed: batchProgress.completed,
      total: batchProgress.total,
    })
    const resultText = t('{{success}} succeeded, {{failed}} failed', {
      success: batchProgress.success,
      failed: batchProgress.failed,
    })

    batchProgressToastIdRef.current = toast.loading(title, {
      id: batchProgressToastIdRef.current ?? undefined,
      description: `${completedText} · ${resultText}`,
    })
  }, [batchProgress, dismissBatchProgressToast, isBatchStopRequested, t])

  useEffect(() => dismissBatchProgressToast, [dismissBatchProgressToast])

  const resetTests = useCallback(() => {
    batchStopRequestedRef.current = true
    setTestResults({})
    setTestingModels(() => new Set())
    setIsBatchTesting(false)
    setIsBatchStopRequested(false)
    setBatchProgress(null)
  }, [])

  const markModelTesting = useCallback((key: string, isTesting: boolean) => {
    setTestingModels((prev) => {
      const next = new Set(prev)
      if (isTesting) {
        next.add(key)
      } else {
        next.delete(key)
      }
      return next
    })
  }, [])

  const updateTestResult = useCallback(
    (key: string, result: TestResult) => {
      onResultChange(key)
      setTestResults((prev) => ({
        ...prev,
        [key]: result,
      }))
    },
    [onResultChange]
  )

  const updateChannelTestCache = useCallback(
    (patch?: ChannelTestCachePatch) => {
      if (!patch) return

      queryClient.setQueriesData<Channel[]>(
        { queryKey: channelsQueryKeys.lists() },
        (oldData) =>
          oldData?.map((channel) =>
            channel.id === currentChannelId
              ? {
                  ...channel,
                  response_time: patch.responseTime,
                  test_time: patch.testTime,
                }
              : channel
          )
      )
    },
    [currentChannelId, queryClient]
  )

  const refreshChannelLists = useCallback(
    (patch?: ChannelTestCachePatch) => {
      updateChannelTestCache(patch)
      void queryClient
        .invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        .then(() => updateChannelTestCache(patch))
        .catch(() => undefined)
    },
    [queryClient, updateChannelTestCache]
  )

  const testSingleModel = useCallback(
    async (
      model: string,
      silent = false,
      refreshList = true
    ): Promise<TestResult | undefined> => {
      if (!currentRow) return

      markModelTesting(model, true)
      updateTestResult(model, { status: 'testing' })
      let finalResult: TestResult | undefined

      try {
        await handleTestChannel(
          currentRow.id,
          {
            channelName: currentRow.name,
            testModel: model,
            endpointType: endpointType === 'auto' ? undefined : endpointType,
            stream: effectiveStreamTest || undefined,
            silent,
          },
          (success, responseTime, error) => {
            const completedAt = Date.now()
            finalResult = {
              status: success ? 'success' : 'error',
              responseTime,
              completedAt,
              error,
            }
            updateTestResult(model, finalResult)
          }
        )
      } catch (error: unknown) {
        finalResult = {
          status: 'error',
          completedAt: Date.now(),
          error: error instanceof Error ? error.message : t('Test failed'),
        }
        updateTestResult(model, finalResult)
      } finally {
        markModelTesting(model, false)
        if (refreshList) {
          refreshChannelLists(
            createChannelTestCachePatch(
              finalResult?.responseTime,
              finalResult?.completedAt
            )
          )
        }
      }
      return finalResult
    },
    [
      currentRow,
      endpointType,
      effectiveStreamTest,
      markModelTesting,
      refreshChannelLists,
      t,
      updateTestResult,
    ]
  )

  const handleStopBatchTest = useCallback(() => {
    if (!isBatchTesting || isBatchStopRequested) return

    batchStopRequestedRef.current = true
    setIsBatchStopRequested(true)
  }, [isBatchStopRequested, isBatchTesting])

  const handleBatchTest = useCallback(
    async (modelsToTest: string[]) => {
      const uniqueModels = [
        ...new Set(modelsToTest.map((model) => model.trim()).filter(Boolean)),
      ]
      if (!uniqueModels.length) return

      batchStopRequestedRef.current = false
      setIsBatchTesting(true)
      setIsBatchStopRequested(false)
      setBatchProgress({
        total: uniqueModels.length,
        completed: 0,
        success: 0,
        failed: 0,
      })

      let resultPatch: ChannelTestCachePatch | undefined
      const results: TestResult[] = []
      let completedCount = 0
      let successCount = 0
      let failedCount = 0

      try {
        const createFallbackResult = (error?: unknown): TestResult => ({
          status: 'error',
          completedAt: Date.now(),
          error: error instanceof Error ? error.message : t('Test failed'),
        })

        const recordBatchResult = (result: TestResult) => {
          results.push(result)
          completedCount += 1
          if (result.status === 'success') {
            successCount += 1
          }
          failedCount = completedCount - successCount

          setBatchProgress({
            total: uniqueModels.length,
            completed: completedCount,
            success: successCount,
            failed: failedCount,
          })
        }

        for (
          let startIndex = 0;
          startIndex < uniqueModels.length;
          startIndex += BATCH_TEST_CONCURRENCY
        ) {
          if (batchStopRequestedRef.current) {
            break
          }

          const batch = uniqueModels.slice(
            startIndex,
            startIndex + BATCH_TEST_CONCURRENCY
          )
          const batchPromises = batch.map(async (modelName) => {
            try {
              const result = await testSingleModel(modelName, true, false)
              const finalResult = result ?? createFallbackResult()
              if (!result) {
                updateTestResult(modelName, finalResult)
              }
              recordBatchResult(finalResult)
              return finalResult
            } catch (error: unknown) {
              const fallbackResult = createFallbackResult(error)
              updateTestResult(modelName, fallbackResult)
              recordBatchResult(fallbackResult)
              return fallbackResult
            }
          })

          await Promise.allSettled(batchPromises)

          if (
            batchStopRequestedRef.current ||
            startIndex + BATCH_TEST_CONCURRENCY >= uniqueModels.length
          ) {
            break
          }

          await sleep(BATCH_TEST_DELAY_MS)
        }

        resultPatch = getLatestChannelTestCachePatch(results)
        const stopped =
          batchStopRequestedRef.current && completedCount < uniqueModels.length

        dismissBatchProgressToast()
        if (stopped) {
          toast.info(
            t(
              'Batch test stopped: {{completed}}/{{total}} completed, {{success}} succeeded, {{failed}} failed',
              {
                completed: completedCount,
                total: uniqueModels.length,
                success: successCount,
                failed: failedCount,
              }
            )
          )
        } else if (failedCount > 0) {
          toast.error(
            t(
              'Batch test completed: {{success}} succeeded, {{failed}} failed',
              {
                success: successCount,
                failed: failedCount,
              }
            )
          )
        } else {
          toast.success(
            t('Batch test completed: {{count}} succeeded', {
              count: successCount,
            })
          )
        }
      } finally {
        batchStopRequestedRef.current = false
        setIsBatchTesting(false)
        setIsBatchStopRequested(false)
        setBatchProgress(null)
        onBatchEnd()
        refreshChannelLists(resultPatch)
      }
    },
    [
      dismissBatchProgressToast,
      onBatchEnd,
      refreshChannelLists,
      t,
      testSingleModel,
      updateTestResult,
    ]
  )

  return {
    testResults,
    testingModels,
    isBatchTesting,
    isBatchStopRequested,
    testSingleModel,
    handleStopBatchTest,
    handleBatchTest,
    resetTests,
  }
}
