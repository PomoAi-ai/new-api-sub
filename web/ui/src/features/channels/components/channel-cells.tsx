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
// Modified 2026-11-04 by new-api-sub: cells extracted from channels-columns.tsx for the template table
import { useTranslation } from 'react-i18next'

import { BadgeListCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { ProviderBadge } from '@/components/provider-badge'
import { StatusBadge } from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import {
  getChannelTypeIcon,
  getChannelTypeLabel,
  parseGroupsList,
  parseModelsList,
} from '../lib/channel-utils'

/** Provider icon + localized channel type name. */
export function ChannelTypeCell({ type }: { type: number }) {
  const { t } = useTranslation()
  const typeName = t(getChannelTypeLabel(type))
  return (
    <TooltipProvider delay={300}>
      <Tooltip>
        <TooltipTrigger
          render={<div className='max-w-full min-w-0 overflow-hidden' />}
        >
          <ProviderBadge
            iconKey={`${getChannelTypeIcon(type)}.Color`}
            iconSize={18}
            label={typeName}
            colorText={false}
            copyable={false}
            showDot={false}
            className='max-w-full min-w-0 overflow-hidden'
          />
        </TooltipTrigger>
        <TooltipContent side='top'>{typeName}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/** Comma-separated model list. */
export function ChannelModelsCell({ models }: { models: string }) {
  return (
    <BadgeListCell
      items={parseModelsList(models).map((model) => (
        <StatusBadge
          key={model}
          label={model}
          autoColor={model}
          size='sm'
          className='font-mono'
        />
      ))}
    />
  )
}

/** Comma-separated group list ('default' first). */
export function ChannelGroupsCell({ groups }: { groups: string }) {
  return (
    <BadgeListCell
      items={parseGroupsList(groups).map((g) => (
        <GroupBadge key={g} group={g} size='sm' />
      ))}
    />
  )
}
