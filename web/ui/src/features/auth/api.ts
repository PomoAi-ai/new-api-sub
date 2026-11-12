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
// Modified 2026-11-06 by new-api-sub: cookie-session login/logout, captcha, second step and passkey sign-in
import axios from 'axios'
import i18next from 'i18next'

import { api, type ApiRequestConfig } from '@/lib/api'
import {
  buildAssertionResult,
  buildRegistrationResult,
  createCredential,
  getCredential,
  prepareCredentialCreationOptions,
  prepareCredentialRequestOptions,
} from '@/lib/passkey'
import type { AuthUser } from '@/stores/auth-store'

import type { Captcha, LoginPayload, LoginPending } from './types'

// 401/403/429 are business errors here (wrong credentials, disabled, locked),
// not an expired session.
const signInRequest: ApiRequestConfig = {
  skipAuthRefresh: true,
  skipErrorHandler: true,
}

// ============================================================================
// Authentication APIs
// ============================================================================

// User login with username and password.
export async function login(
  payload: LoginPayload
): Promise<AuthUser | LoginPending> {
  const res = await api.post<AuthUser | LoginPending>(
    '/api/auth/login',
    payload,
    signInRequest
  )
  return res.data
}

/** Required after repeated failures; one answer per captcha, right or wrong. */
export async function getCaptcha(): Promise<Captcha> {
  return (
    await api.get<Captcha>('/api/auth/captcha', {
      ...signInRequest,
      disableDuplicate: true,
    })
  ).data
}

/** The server asks for a captcha with 428, or with 401 on the failure before. */
export function isCaptchaRequired(error: unknown): boolean {
  return (
    axios.isAxiosError(error) &&
    (error.response?.data as { captcha_required?: unknown } | undefined)
      ?.captcha_required === true
  )
}

/** For a 429 with Retry-After: says how long to wait instead of the raw error. */
export function waitPresentation(
  error: unknown
): { title: string } | undefined {
  if (!axios.isAxiosError(error) || error.response?.status !== 429) {
    return undefined
  }
  const seconds = Number(error.response.headers['retry-after'])
  if (!Number.isFinite(seconds) || seconds <= 0) return undefined
  return {
    title: i18next.t(
      'Too many attempts. Try again in {{time}}.',
      seconds < 60
        ? { time: i18next.t('{{count}} seconds', { count: seconds }) }
        : {
            time: i18next.t('{{count}} minutes', {
              count: Math.ceil(seconds / 60),
            }),
          }
    ),
  }
}

/** Finishes a pending login with an authenticator or a backup code. */
export async function loginSecondStep(
  body: { flow_token: string } & ({ code: string } | { backup_code: string })
): Promise<void> {
  await api.post('/api/auth/login/2fa', body, signInRequest)
}

/** Signs in as the passkey's owner, with or without a pending login. */
export async function signInWithPasskey(): Promise<void> {
  const { data } = await api.post<{ flow_token: string; options: unknown }>(
    '/api/auth/passkey/begin',
    undefined,
    signInRequest
  )
  const credential = await assertPasskey(data.options)
  await api.post(
    '/api/auth/passkey/finish',
    { flow_token: data.flow_token, credential },
    signInRequest
  )
}

// User logout (204 No Content)
export async function logout(): Promise<void> {
  await api.post('/api/auth/logout', undefined, signInRequest)
}

// ============================================================================
// Browser passkey prompts
// ============================================================================

/** The browser's own DOMException text is not fit to show to users. */
async function promptPasskey<T>(prompt: () => Promise<T>): Promise<T> {
  try {
    return await prompt()
  } catch (error) {
    if (!(error instanceof DOMException)) throw error
    throw new Error(
      i18next.t(
        error.name === 'NotAllowedError'
          ? 'The passkey prompt was cancelled or timed out.'
          : 'This device could not complete the passkey step.'
      )
    )
  }
}

export function assertPasskey(options: unknown) {
  return promptPasskey(async () =>
    buildAssertionResult(
      (await getCredential(
        prepareCredentialRequestOptions(options)
      )) as PublicKeyCredential | null
    )
  )
}

export function registerPasskey(options: unknown) {
  return promptPasskey(async () =>
    buildRegistrationResult(
      (await createCredential(
        prepareCredentialCreationOptions(options)
      )) as PublicKeyCredential | null
    )
  )
}
