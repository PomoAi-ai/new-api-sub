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
import { ApiDocs } from '@app/features/api-tokens/api-docs'
import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { PublicPage } from '@/features/home'

function DocsPage() {
  const { t } = useTranslation()
  return (
    <PublicPage>
      <div className='mx-auto max-w-4xl px-6 pt-28 pb-16'>
        <h1 className='mb-8 text-2xl font-bold tracking-tight'>
          {t('API Docs')}
        </h1>
        <ApiDocs />
      </div>
    </PublicPage>
  )
}

export const Route = createFileRoute('/docs')({ component: DocsPage })
