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
import { assertPasskey, registerPasskey } from '@/features/auth/api'
import type { FactorMethod } from '@/features/auth/types'
import { api, type ApiRequestConfig } from '@/lib/api'

export type SecurityStatus = {
  totp: boolean
  passkey: boolean
  backup_codes_left: number
  methods: FactorMethod[]
}

export type ProofPurpose =
  | 'totp_bind'
  | 'totp_unbind'
  | 'passkey_bind'
  | 'passkey_unbind'
  | 'backup_regenerate'
  | 'token_create'
  | 'newapi_config'

export type FactorAnswer =
  | { code: string }
  | { backup_code: string }
  | { password: string }

export type TotpSetup = {
  flow_token: string
  otpauth_url: string
  secret: string
}

type Proof = { proof: string; expires_in: number }
type BackupCodes = { backup_codes?: string[] }

/** useProof's ask() rejects with this when the user backs out; callers just stop. */
export class ProofCancelled extends Error {}

export const securityQueryKey = ['account-security'] as const

/** A proof goes straight from the prompt to this header, never into state. */
export function withProof(proof: string): ApiRequestConfig {
  return { headers: { 'X-Security-Proof': proof } }
}

export async function getSecurityStatus(): Promise<SecurityStatus> {
  return (await api.get<SecurityStatus>('/api/me/security')).data
}

export async function requestProof(
  purpose: ProofPurpose,
  answer: FactorAnswer
): Promise<string> {
  const { data } = await api.post<Proof>('/api/me/security/proof', {
    purpose,
    ...answer,
  })
  return data.proof
}

export async function requestPasskeyProof(
  purpose: ProofPurpose
): Promise<string> {
  const { data: begun } = await api.post<{
    flow_token: string
    options: unknown
  }>('/api/me/security/proof/passkey/begin', { purpose })
  const credential = await assertPasskey(begun.options)
  const { data } = await api.post<Proof>(
    '/api/me/security/proof/passkey/finish',
    { flow_token: begun.flow_token, credential }
  )
  return data.proof
}

export async function startTotpSetup(proof: string): Promise<TotpSetup> {
  return (
    await api.post<TotpSetup>(
      '/api/me/security/totp/setup',
      undefined,
      withProof(proof)
    )
  ).data
}

/** Backup codes come back only when this is the account's first factor. */
export async function confirmTotp(
  flowToken: string,
  code: string
): Promise<string[] | undefined> {
  const { data } = await api.post<BackupCodes>(
    '/api/me/security/totp/confirm',
    {
      flow_token: flowToken,
      code,
    }
  )
  return data.backup_codes
}

export async function unbindTotp(proof: string): Promise<void> {
  await api.delete('/api/me/security/totp', withProof(proof))
}

/** Backup codes come back only when this is the account's first factor. */
export async function bindPasskey(
  proof: string
): Promise<string[] | undefined> {
  const { data: begun } = await api.post<{
    flow_token: string
    options: unknown
  }>('/api/me/security/passkey/begin', undefined, withProof(proof))
  const credential = await registerPasskey(begun.options)
  const { data } = await api.post<BackupCodes>(
    '/api/me/security/passkey/finish',
    { flow_token: begun.flow_token, credential }
  )
  return data.backup_codes
}

export async function unbindPasskey(proof: string): Promise<void> {
  await api.delete('/api/me/security/passkey', withProof(proof))
}

export async function regenerateBackupCodes(proof: string): Promise<string[]> {
  const { data } = await api.post<{ backup_codes: string[] }>(
    '/api/me/security/backup-codes',
    undefined,
    withProof(proof)
  )
  return data.backup_codes
}
