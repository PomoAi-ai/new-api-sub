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
import { REGEXP_ONLY_DIGITS } from 'input-otp'
import { Fingerprint, Loader2 } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSlot,
} from '@/components/ui/input-otp'
import { waitPresentation } from '@/features/auth/api'
import { usePasskeySupported } from '@/features/auth/lib/use-passkey-supported'
import type { FactorMethod } from '@/features/auth/types'
import { handleServerError } from '@/lib/handle-server-error'

import type { FactorAnswer } from './api'

const TOTP_LENGTH = 6
const TEXT_MODES = ['totp', 'backup', 'password'] as const
type TextMode = (typeof TEXT_MODES)[number]

export function CodeInput(props: {
  value: string
  disabled?: boolean
  onChange: (value: string) => void
  onComplete: (value: string) => void
}) {
  const { t } = useTranslation()
  return (
    <InputOTP
      maxLength={TOTP_LENGTH}
      pattern={REGEXP_ONLY_DIGITS}
      inputMode='numeric'
      autoComplete='one-time-code'
      autoFocus
      aria-label={t('Verification code')}
      containerClassName='justify-center'
      value={props.value}
      disabled={props.disabled}
      onChange={props.onChange}
      onComplete={props.onComplete}
    >
      <InputOTPGroup>
        {Array.from({ length: TOTP_LENGTH }, (_, index) => (
          <InputOTPSlot key={index} index={index} className='size-10' />
        ))}
      </InputOTPGroup>
    </InputOTP>
  )
}

/**
 * Asks for one of the given factors. The typed value stays in this component
 * and is dropped on failure; failures are reported here, success is the
 * caller's to act on.
 */
export function FactorForm(props: {
  methods: FactorMethod[]
  submitText: string
  onAnswer: (answer: FactorAnswer) => Promise<void>
  onPasskey: () => Promise<void>
}) {
  const { t } = useTranslation()
  const passkeySupported = usePasskeySupported()
  const modes = TEXT_MODES.filter((mode) => props.methods.includes(mode))
  const [mode, setMode] = useState<TextMode | undefined>(modes[0])
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const showPasskey = passkeySupported && props.methods.includes('passkey')

  const run = async (action: () => Promise<void>) => {
    setBusy(true)
    try {
      await action()
    } catch (error) {
      setValue('')
      handleServerError(
        error,
        t('Verification failed'),
        waitPresentation(error)
      )
    } finally {
      setBusy(false)
    }
  }

  const answer = (secret: string) => {
    if (!mode || !secret || busy) return
    const answers: Record<TextMode, FactorAnswer> = {
      totp: { code: secret },
      backup: { backup_code: secret.trim() },
      password: { password: secret },
    }
    void run(() => props.onAnswer(answers[mode]))
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    answer(value)
  }
  const switchMode = (next: TextMode) => {
    setValue('')
    setMode(next)
  }

  return (
    <div className='grid gap-3'>
      {mode && (
        <form onSubmit={submit} className='grid gap-3'>
          {mode === 'totp' && (
            <>
              <p className='text-muted-foreground text-sm'>
                {t('Enter the 6-digit code from your authenticator app.')}
              </p>
              <CodeInput
                value={value}
                disabled={busy}
                onChange={setValue}
                onComplete={answer}
              />
            </>
          )}
          {mode === 'backup' && (
            <Input
              aria-label={t('Backup code')}
              placeholder={t('Backup code')}
              autoComplete='off'
              spellCheck={false}
              autoFocus
              className='font-mono'
              value={value}
              disabled={busy}
              onChange={(event) => setValue(event.target.value)}
            />
          )}
          {mode === 'password' && (
            <PasswordInput
              aria-label={t('Password')}
              placeholder={t('Enter password')}
              autoComplete='current-password'
              autoFocus
              value={value}
              disabled={busy}
              onChange={(event) => setValue(event.target.value)}
            />
          )}
          <Button type='submit' disabled={busy || !value}>
            {busy && <Loader2 className='size-4 animate-spin' />}
            {props.submitText}
          </Button>
        </form>
      )}
      {showPasskey && (
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() => void run(props.onPasskey)}
        >
          <Fingerprint className='size-4' />
          {t('Use a passkey')}
        </Button>
      )}
      {mode === 'totp' && modes.includes('backup') && (
        <Button
          type='button'
          variant='link'
          size='sm'
          onClick={() => switchMode('backup')}
        >
          {t('Use a backup code')}
        </Button>
      )}
      {mode === 'backup' && modes.includes('totp') && (
        <Button
          type='button'
          variant='link'
          size='sm'
          onClick={() => switchMode('totp')}
        >
          {t('Use an authenticator code')}
        </Button>
      )}
      {!mode && !showPasskey && (
        <p className='text-muted-foreground text-sm'>
          {t('No verification method is available on this device.')}
        </p>
      )}
    </div>
  )
}
