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
import {
  LayoutDashboard,
  ClipboardList,
  FileText,
  KeyRound,
  LayoutTemplate,
  Radio,
  Settings,
  ShieldCheck,
  UserCog,
  UserPen,
  Users,
} from 'lucide-react'
import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'

import type { SidebarData } from '@/components/layout/types'
import type { ProfileMenuItem } from '@/components/profile-dropdown'
import { useAuthStore } from '@/stores/auth-store'

// Keep this module light: lib/constants re-exports APP_NAME, so statically
// importing feature code here can create import cycles. Load dialogs with
// React.lazy when adding profile menu items.

export const APP_NAME = '小蓝号池管理后台'

/** Post-login landing page (console home). */
export const LANDING_PATH = '/users'

const ChangePasswordDialog = lazy(() =>
  import('@/features/account/change-password-dialog').then((m) => ({
    default: m.ChangePasswordDialog,
  }))
)
const ChangeUsernameDialog = lazy(() =>
  import('./features/account/change-username-dialog').then((m) => ({
    default: m.ChangeUsernameDialog,
  }))
)
// oxlint-disable-next-line react/only-export-components -- lazy dialog breaks the lib/constants import cycle
const AccountSecurityDialog = lazy(() =>
  import('@/features/account/security/account-security-dialog').then((m) => ({
    default: m.AccountSecurityDialog,
  }))
)

/** Sidebar navigation by role; keep LANDING_PATH as the first item. */
export function useSidebarData(): SidebarData {
  const { t } = useTranslation()
  const isSuper = useAuthStore((s) => s.auth.user?.role === 'super')
  return {
    navGroups: [
      {
        id: 'general',
        title: t('Console'),
        items: [
          { title: t('Users'), url: '/users', icon: Users },
          ...(isSuper
            ? [
                { title: t('Admins'), url: '/admins', icon: UserCog },
                {
                  title: t('Templates'),
                  url: '/templates',
                  icon: LayoutTemplate,
                },
                {
                  title: t('System Settings'),
                  url: '/settings',
                  icon: Settings,
                },
              ]
            : []),
          { title: t('Channels'), url: '/channels', icon: Radio },
          { title: t('Usage Logs'), url: '/logs', icon: FileText },
          { title: t('Dashboard'), url: '/stats', icon: LayoutDashboard },
          {
            title: t('Audit Logs'),
            url: '/audit-logs',
            icon: ClipboardList,
          },
        ],
      },
    ],
  }
}

/** Extra profile dropdown entries, shown above Sign out. */
export const profileMenuItems: ProfileMenuItem[] = [
  {
    title: 'Change Account',
    icon: UserPen,
    superOnly: true,
    dialog: (props) => (
      <Suspense fallback={null}>
        <ChangeUsernameDialog {...props} />
      </Suspense>
    ),
  },
  {
    title: 'Change Password',
    icon: KeyRound,
    dialog: (props) => (
      <Suspense fallback={null}>
        <ChangePasswordDialog {...props} />
      </Suspense>
    ),
  },
  {
    title: 'Account Security',
    icon: ShieldCheck,
    dialog: (props) => (
      <Suspense fallback={null}>
        <AccountSecurityDialog {...props} />
      </Suspense>
    ),
  },
]
