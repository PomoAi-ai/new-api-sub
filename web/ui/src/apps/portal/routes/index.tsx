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
import { createFileRoute, Link } from '@tanstack/react-router'
import {
  BookOpen,
  FileText,
  LayoutDashboard,
  Radio,
  ReceiptText,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { HomePage } from '@/features/home'

function DocsLink() {
  const { t } = useTranslation()
  return (
    <Button
      variant='outline'
      size='sm'
      className='h-11 rounded-lg px-5 text-sm font-medium'
      render={<Link to='/docs' />}
    >
      <BookOpen className='mr-1.5 size-4' />
      {t('API Docs')}
    </Button>
  )
}

export const Route = createFileRoute('/')({
  component: () => (
    <HomePage
      badge='Supplier portal'
      title='Add your channels to the pool'
      highlight='Usage and settlements made clear'
      description='Add channels from templates, follow usage logs and daily statistics, and check every settlement in one place.'
      features={[
        {
          icon: Radio,
          title: 'Template-based channels',
          description:
            'Pick a template and fill in your key; models and settings are configured for you.',
        },
        {
          icon: FileText,
          title: 'Real-time usage logs',
          description:
            'See every request on your channels: model, tokens, and status.',
        },
        {
          icon: LayoutDashboard,
          title: 'Daily statistics',
          description: 'Requests and quota consumed, by day and by channel.',
        },
        {
          icon: ReceiptText,
          title: 'Transparent settlements',
          description: 'Each settlement lists its period and amount.',
        },
      ]}
      actions={<DocsLink />}
      steps={[
        {
          title: 'Get an account',
          description: 'Your administrator creates an account for you.',
        },
        {
          title: 'Add channels',
          description: 'Sign in and add channels from templates.',
        },
        {
          title: 'Track and settle',
          description: 'Follow usage and reconcile each settlement.',
        },
      ]}
    />
  ),
})
