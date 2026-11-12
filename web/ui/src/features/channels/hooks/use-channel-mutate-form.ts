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
// Modified 2026-11-08 by new-api-sub: create only, role-aware payload; POST /api/channels answers {ids}, pushed to the pools afterwards
import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { createChannel } from '../api'
import { ERROR_MESSAGES } from '../constants'
import {
  transformFormDataToCreatePayload,
  type ChannelFormValues,
} from '../lib'
import type { ChannelsMode } from '../types'

type UseChannelMutateFormParams = {
  mode: ChannelsMode
  onSuccess: () => void
}

export function useChannelMutateForm(props: UseChannelMutateFormParams) {
  const { t } = useTranslation()

  return useMutation({
    mutationFn: (data: ChannelFormValues) =>
      createChannel(transformFormDataToCreatePayload(data, props.mode)),
    onSuccess: ({ ids }) => {
      // The channels are saved here and pushed to their pools in the background.
      toast.success(
        ids.length > 1
          ? t('{{count}} channels submitted, pushing to the pools', {
              count: ids.length,
            })
          : t('Submitted, pushing to the pools')
      )
      props.onSuccess()
    },
    onError: (error: unknown) => {
      handleServerError(error, t(ERROR_MESSAGES.CREATE_FAILED))
    },
  })
}
