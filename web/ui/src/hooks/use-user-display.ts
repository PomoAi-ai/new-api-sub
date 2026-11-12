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
// Modified 2026-11-04 by new-api-sub: users only have id and username
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { AuthUser } from '@/stores/auth-store'

const ROLE_LABELS = { super: 'Super Admin', admin: 'Admin' } as const

/**
 * Custom hook to format user display information
 * Centralizes user display logic used across ProfileDropdown and MobileDrawer
 */
export function useUserDisplay(user: AuthUser | null | undefined) {
  const { t } = useTranslation()
  return useMemo(() => {
    if (!user) {
      return {
        displayName: t('User'),
        secondaryText: '',
        initials: 'U',
        roleLabel: '',
      }
    }

    // Display name: priority order
    const displayName = user.username || t('User')

    const secondaryText = user.username

    // Generate initials from display name
    const initials = displayName
      .split(' ')
      .map((n: string) => n[0])
      .join('')
      .toUpperCase()
      .slice(0, 2)

    // Portal users have no role field.
    const roleLabel = t(user.role ? ROLE_LABELS[user.role] : 'Common User')

    return {
      displayName,
      secondaryText,
      initials,
      roleLabel,
    }
  }, [user, t])
}
