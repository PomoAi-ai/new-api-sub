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
import { createFileRoute } from '@tanstack/react-router'
import {
  ClipboardList,
  LayoutDashboard,
  LayoutTemplate,
  Users,
} from 'lucide-react'

import { HomePage } from '@/features/home'

export const Route = createFileRoute('/')({
  component: () => (
    <HomePage
      badge='Admin console'
      title='Manage suppliers and channels'
      highlight='All in one place'
      description='Open supplier accounts, maintain channel templates, and review usage, statistics, and audit logs across all suppliers.'
      features={[
        {
          icon: Users,
          title: 'Supplier accounts',
          description:
            'Create and disable supplier accounts and reset their passwords.',
        },
        {
          icon: LayoutTemplate,
          title: 'Channel templates',
          description:
            'Define the models and settings suppliers use when adding channels.',
        },
        {
          icon: LayoutDashboard,
          title: 'Usage and statistics',
          description: 'Usage logs and daily statistics across all channels.',
        },
        {
          icon: ClipboardList,
          title: 'Audit logs',
          description: 'Every change by admins and suppliers is recorded.',
        },
      ]}
      steps={[
        {
          title: 'Set up templates',
          description: 'Define channel templates and their models.',
        },
        {
          title: 'Open accounts',
          description: 'Create supplier accounts and hand them over.',
        },
        {
          title: 'Review and settle',
          description: 'Review usage and settle with suppliers.',
        },
      ]}
    />
  ),
})
