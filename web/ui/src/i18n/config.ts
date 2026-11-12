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
// Modified 2026-11-04 by new-api-sub: only en and zhCN; merge app-specific zh translations
import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import accountSecurity from './app/account-security.zh.json'
import adminAdmins from './app/admin-admins.zh.json'
import adminAudit from './app/admin-audit.zh.json'
import adminTemplates from './app/admin-templates.zh.json'
import adminUsers from './app/admin-users.zh.json'
import apiDocs from './app/api-docs.zh.json'
import channels from './app/channels.zh.json'
import home from './app/home.zh.json'
import logs from './app/logs.zh.json'
import portalAccount from './app/portal-account.zh.json'
import supplierConsole from './app/supplier-console.zh.json'
import { convertDetectedLanguage } from './languages'
import en from './locales/en.json'
import zh from './locales/zh.json'

// Keys are English source text; app files hold only the zh translations.
const zhCN = {
  translation: {
    ...zh.translation,
    ...channels,
    ...logs,
    ...portalAccount,
    ...accountSecurity,
    ...apiDocs,
    ...adminUsers,
    ...adminAdmins,
    ...adminTemplates,
    ...adminAudit,
    ...supplierConsole,
    ...home,
  },
}

export const resources = {
  en,
  zhCN,
} as const

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    supportedLngs: ['en', 'zhCN'],
    load: 'currentOnly',
    nsSeparator: false, // Allow literal colons in keys (e.g., URLs, labels)
    debug: import.meta.env.DEV,
    interpolation: {
      escapeValue: false, // not needed for react as it escapes by default
    },
    detection: {
      order: ['localStorage', 'navigator'],
      caches: ['localStorage'],
      // Browsers report `zh-CN`/`zh-TW`/`zh`; map them onto our `zhCN`/`zhTW`
      // codes (non-Chinese codes pass through for normal supportedLngs matching).
      convertDetectedLanguage,
    },
  })

export default i18n
