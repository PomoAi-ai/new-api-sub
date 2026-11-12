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
// Modified 2026-11-04 by new-api-sub: from section-registry.tsx; the sections are a search parameter of /stats, without the overview
import { z } from 'zod'

export const DASHBOARD_SECTION_IDS = ['models', 'flow', 'users'] as const

export type DashboardSectionId = (typeof DASHBOARD_SECTION_IDS)[number]

export const DASHBOARD_DEFAULT_SECTION: DashboardSectionId = 'models'

export const dashboardSearchSchema = z.object({
  section: z.enum(DASHBOARD_SECTION_IDS).optional().catch(undefined),
})
