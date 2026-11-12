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
// Modified 2026-11-08 by new-api-sub: the common-log section only, shared by both apps; notes pools left out of the data and the synced pools' 30 days
import { getRouteApi } from '@tanstack/react-router'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  DegradedPoolsAlert,
  RetentionAlert,
} from './components/degraded-pools-alert'
import {
  type LogsViewScope,
  UsageLogsProvider,
  useLogsViewScope,
} from './components/usage-logs-provider'
import { UsageLogsTable } from './components/usage-logs-table'
import type { LogsMode } from './types'

export { logsSearchSchema } from './constants'

const route = getRouteApi('/_authenticated/logs/')

function UsageLogsContent() {
  const { t } = useTranslation()
  const navigate = route.useNavigate()
  const { viewScope, setViewScope, selfOnly } = useLogsViewScope()

  const handleViewScopeChange = useCallback(
    (scope: string) => {
      if (scope === 'all' || scope === 'self') {
        setViewScope(scope as LogsViewScope)
        // A channel of one view can be out of the other's scope.
        void navigate({ search: (prev) => ({ ...prev, channel: undefined }) })
      }
    },
    [navigate, setViewScope]
  )

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Common Logs')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Tabs value={viewScope} onValueChange={handleViewScopeChange}>
          <TabsList>
            {!selfOnly && <TabsTrigger value='all'>{t('All')}</TabsTrigger>}
            <TabsTrigger value='self'>{t('Only Mine')}</TabsTrigger>
          </TabsList>
        </Tabs>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-4'>
          <DegradedPoolsAlert prefix='logs:' />
          <RetentionAlert prefix='logs:' />
          <div className='min-h-0 flex-1'>
            <UsageLogsTable />
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

/** The log page of both apps. */
export function Logs(props: { mode: LogsMode }) {
  return (
    <UsageLogsProvider mode={props.mode}>
      <UsageLogsContent />
    </UsageLogsProvider>
  )
}
