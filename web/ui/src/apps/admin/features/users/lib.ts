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
import { z } from 'zod'

// Portal users and admins share the backend account rules.
export const usernameSchema = z
  .string()
  .regex(
    /^[A-Za-z0-9_.-]{3,32}$/,
    'Username must be 3–32 letters, digits or _.-'
  )

// Mirrors the backend rule (bcrypt limit): 8–72 bytes of UTF-8.
export const passwordSchema = z.string().refine((password) => {
  const bytes = new TextEncoder().encode(password).length
  return bytes >= 8 && bytes <= 72
}, 'Password must be 8–72 bytes')

// No look-alike characters (0/O, 1/l/I): operators read these out as well as paste them.
const PASSWORD_CHARS = 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789' // gitleaks:allow
const PASSWORD_LENGTH = 16

export function randomPassword(): string {
  // Rejecting the top of the byte range keeps every character equally likely.
  const limit = 256 - (256 % PASSWORD_CHARS.length)
  let out = ''
  while (out.length < PASSWORD_LENGTH) {
    for (const byte of crypto.getRandomValues(
      new Uint8Array(PASSWORD_LENGTH)
    )) {
      if (byte < limit && out.length < PASSWORD_LENGTH) {
        out += PASSWORD_CHARS[byte % PASSWORD_CHARS.length]
      }
    }
  }
  return out
}
