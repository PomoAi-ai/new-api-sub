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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'

import { getOwnSettlements } from './api'
import { SettlementOverview } from './components/settlement-overview'

/** The portal's read-only view of the user's own settlements. */
export function Settlements() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['own-settlements'],
    queryFn: getOwnSettlements,
    retry: 1,
    meta: { errorToast: false, errorRedirect: false },
  })

  let content = <LoadingState message={t('Loading...')} />
  if (query.isError) {
    content = (
      <ErrorState
        title={t('Failed to load settlement records')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data) {
    content = <SettlementOverview overview={query.data} />
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Settlements')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
