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
// Modified 2026-11-04 by new-api-sub: only previews a form not saved yet, through the super-only proxy
import { useCallback, useEffect, useRef, useState } from 'react'

import { fetchChannelModels } from '../api'

type DiscoveryState = {
  status: 'idle' | 'loading' | 'success' | 'error' | 'stale'
  models: string[]
  error?: unknown
}

export type ChannelModelDiscoveryRequest = Parameters<
  typeof fetchChannelModels
>[0]

type ChannelModelDiscoveryProps = {
  enabled: boolean
  request: ChannelModelDiscoveryRequest
}

export function useChannelModelDiscovery(props: ChannelModelDiscoveryProps) {
  const [state, setState] = useState<DiscoveryState>({
    status: 'idle',
    models: [],
  })
  const sequence = useRef(0)

  useEffect(() => {
    sequence.current += 1
    setState((previous) => {
      if (previous.status === 'idle') return previous
      if (!props.enabled) return { status: 'idle', models: [] }
      return { status: 'stale', models: previous.models }
    })
    return () => {
      sequence.current += 1
    }
  }, [props.enabled, props.request])

  const fetch = useCallback(async () => {
    if (!props.enabled) return
    const requestSequence = ++sequence.current
    setState((previous) => ({ status: 'loading', models: previous.models }))
    try {
      const models = await fetchChannelModels(props.request)
      if (requestSequence !== sequence.current) return
      setState({ status: 'success', models })
    } catch (error) {
      if (requestSequence !== sequence.current) return
      setState((previous) => ({
        status: 'error',
        models: previous.models,
        error,
      }))
    }
  }, [props.enabled, props.request])

  return { ...state, fetch }
}
