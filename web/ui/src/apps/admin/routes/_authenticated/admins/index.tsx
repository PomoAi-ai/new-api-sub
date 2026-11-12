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
import { Admins } from '@app/features/admins'
import { createFileRoute, redirect } from '@tanstack/react-router'
import z from 'zod'

import { useAuthStore } from '@/stores/auth-store'

const adminsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(undefined),
  filter: z.string().optional().catch(''),
  status: z
    .array(z.enum(['1', '2']))
    .optional()
    .catch([]),
  role: z
    .array(z.enum(['super', 'admin']))
    .optional()
    .catch([]),
})

export const Route = createFileRoute('/_authenticated/admins/')({
  beforeLoad: () => {
    if (useAuthStore.getState().auth.user?.role !== 'super') {
      throw redirect({ to: '/403' })
    }
  },
  validateSearch: adminsSearchSchema,
  component: Admins,
})
