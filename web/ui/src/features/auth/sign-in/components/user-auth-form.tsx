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
// Modified 2026-11-06 by new-api-sub: username/password with a captcha when asked, an optional second step, and passkey sign-in (no OAuth, WeChat, Turnstile, legal consent, forgot password); default landing is LANDING_PATH
import { LANDING_PATH } from '@app/app-config'
import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import { Fingerprint, Loader2, LogIn, RefreshCw } from 'lucide-react'
import { useCallback, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { z } from 'zod'

import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  getCaptcha,
  isCaptchaRequired,
  login,
  signInWithPasskey,
  waitPresentation,
} from '@/features/auth/api'
import { loginFormSchema } from '@/features/auth/constants'
import { sanitizeAuthRedirect } from '@/features/auth/lib/auth-redirect'
import { usePasskeySupported } from '@/features/auth/lib/use-passkey-supported'
import type {
  AuthFormProps,
  Captcha,
  LoginPending,
} from '@/features/auth/types'
import { clearMfaReminder, resolveAuthentication } from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { SecondStepForm } from './second-step-form'

export function UserAuthForm({
  className,
  redirectTo,
  ...props
}: AuthFormProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [isLoading, setIsLoading] = useState(false)
  const [pending, setPending] = useState<LoginPending | null>(null)
  const passkeySupported = usePasskeySupported()
  // Shown only after the server asks for it; each one answers a single try.
  const [captcha, setCaptcha] = useState<Captcha | null>(null)
  const [captchaCode, setCaptchaCode] = useState('')
  const loginFailedMessage = t('Login failed')

  const loadCaptcha = async () => {
    setCaptchaCode('')
    try {
      setCaptcha(await getCaptcha())
    } catch (error) {
      setCaptcha(null)
      handleServerError(
        error,
        t('Failed to load the captcha'),
        waitPresentation(error)
      )
    }
  }

  const showLoginError = (error: unknown) =>
    handleServerError(error, loginFailedMessage, waitPresentation(error))

  const form = useForm<z.infer<typeof loginFormSchema>>({
    resolver: zodResolver(loginFormSchema),
    defaultValues: {
      username: '',
      password: '',
    },
  })

  // Every way in ends here, once the session cookie is set.
  async function finishLogin() {
    clearMfaReminder()
    // Load /api/me (user + quota_per_unit) for the new session.
    useAuthStore.getState().auth.reset('idle')
    await resolveAuthentication()
    toast.success(t('Welcome back!'))
    const target =
      sanitizeAuthRedirect(redirectTo, window.location.origin) ?? LANDING_PATH
    await navigate({ href: target, replace: true })
  }

  async function onSubmit(data: z.infer<typeof loginFormSchema>) {
    setIsLoading(true)
    try {
      const result = await login({
        username: data.username,
        password: data.password,
        ...(captcha && {
          captcha_id: captcha.captcha_id,
          captcha_code: captchaCode.trim(),
        }),
      })
      form.setValue('password', '')
      setCaptcha(null)
      setCaptchaCode('')
      if ('mfa_required' in result) setPending(result)
      else await finishLogin()
    } catch (error: unknown) {
      showLoginError(error)
      // A captcha is used up by any answer, so a shown one is always replaced.
      if (captcha || isCaptchaRequired(error)) await loadCaptcha()
    } finally {
      setIsLoading(false)
    }
  }

  async function onPasskey() {
    setIsLoading(true)
    try {
      await signInWithPasskey()
      await finishLogin()
    } catch (error: unknown) {
      showLoginError(error)
    } finally {
      setIsLoading(false)
    }
  }

  const backToPassword = useCallback(() => setPending(null), [])

  if (pending) {
    return (
      <SecondStepForm
        pending={pending}
        onBack={backToPassword}
        onSuccess={finishLogin}
      />
    )
  }

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(onSubmit)}
        className={cn('grid gap-4', className)}
        {...props}
      >
        {/* Username Field */}
        <FormField
          control={form.control}
          name='username'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Username')}</FormLabel>
              <FormControl>
                <Input
                  placeholder={t('Enter your username')}
                  autoComplete='username'
                  {...field}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        {/* Password Field */}
        <FormField
          control={form.control}
          name='password'
          render={({ field }) => (
            <FormItem className='relative'>
              <FormLabel>{t('Password')}</FormLabel>
              <FormControl>
                <PasswordInput
                  placeholder={t('Enter password')}
                  autoComplete='current-password'
                  {...field}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        {captcha && (
          <div className='grid gap-2'>
            <Label htmlFor='login-captcha'>{t('Captcha')}</Label>
            <div className='flex items-center gap-2'>
              <Input
                id='login-captcha'
                inputMode='numeric'
                autoComplete='off'
                spellCheck={false}
                maxLength={6}
                placeholder={t('Enter the digits in the image')}
                value={captchaCode}
                onChange={(event) => setCaptchaCode(event.target.value)}
              />
              <img
                src={captcha.image}
                alt={t('Captcha')}
                className='h-9 w-28 shrink-0 rounded-md border bg-white object-contain'
              />
              <Button
                type='button'
                variant='ghost'
                size='icon'
                disabled={isLoading}
                aria-label={t('New image')}
                title={t('New image')}
                onClick={() => void loadCaptcha()}
              >
                <RefreshCw />
              </Button>
            </div>
          </div>
        )}

        {/* Submit Button */}
        <Button
          type='submit'
          className='mt-2 w-full justify-center gap-2'
          disabled={isLoading || (captcha !== null && !captchaCode.trim())}
        >
          {isLoading ? <Loader2 className='animate-spin' /> : <LogIn />}
          {t('Sign in')}
        </Button>

        {passkeySupported && (
          <Button
            type='button'
            variant='outline'
            className='w-full justify-center gap-2'
            disabled={isLoading}
            onClick={() => void onPasskey()}
          >
            <Fingerprint />
            {t('Sign in with a passkey')}
          </Button>
        )}
      </form>
    </Form>
  )
}
