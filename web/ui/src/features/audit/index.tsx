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
// Modified 2026-11-04 by new-api-sub: the audit page of both apps; only the admin app has the "Only Mine" view
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import type { AuditMode } from './api'
import { AuditLogViewer } from './components/audit-log-viewer'

export function AuditLogs(props: { mode: AuditMode }) {
  const { t } = useTranslation()
  const [scope, setScope] = useState<'all' | 'self'>('all')
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Audit Logs')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {props.mode === 'admin' && (
          <Tabs
            value={scope}
            onValueChange={(value) =>
              setScope(value === 'all' ? 'all' : 'self')
            }
          >
            <TabsList aria-label={t('View scope')}>
              <TabsTrigger value='all'>{t('All')}</TabsTrigger>
              <TabsTrigger value='self'>{t('Only Mine')}</TabsTrigger>
            </TabsList>
          </Tabs>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-3'>
          <div className='min-h-0 flex-1'>
            <AuditLogViewer key={scope} mode={props.mode} scope={scope} />
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
