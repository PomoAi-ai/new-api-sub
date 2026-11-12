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
// Modified 2026-11-04 by new-api-sub: admin resets a portal user's or admin's password; adapted from security/components/dialogs/change-password-dialog.tsx
import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
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
import { handleServerError } from '@/lib/handle-server-error'

import { passwordSchema } from '../lib'

const passwordResetSchema = z
  .object({
    newPassword: passwordSchema,
    confirmPassword: z.string(),
  })
  .refine((data) => data.newPassword === data.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })
type PasswordResetValues = z.infer<typeof passwordResetSchema>
const emptyPasswordForm: PasswordResetValues = {
  newPassword: '',
  confirmPassword: '',
}

type UserPasswordDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  account: { id: number; username: string } | null
  resetPassword: (id: number, password: string) => Promise<void>
}

export function UserPasswordDialog(props: UserPasswordDialogProps) {
  const { t } = useTranslation()
  const [pending, setPending] = useState(false)
  const form = useForm<PasswordResetValues>({
    resolver: zodResolver(passwordResetSchema),
    defaultValues: emptyPasswordForm,
  })

  useEffect(() => {
    if (!props.open) form.reset(emptyPasswordForm)
  }, [props.open, form])

  const submit = async (values: PasswordResetValues) => {
    if (!props.account) return
    setPending(true)
    try {
      await props.resetPassword(props.account.id, values.newPassword)
      toast.success(t('Password reset successfully'))
      props.onOpenChange(false)
    } catch (error) {
      handleServerError(error, t('Failed to reset password'))
    } finally {
      setPending(false)
    }
  }
  const title = t('Reset Password')

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={title}
      description={
        <>
          {t('Set a new password for account:')}{' '}
          <strong>{props.account?.username}</strong>
        </>
      }
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form='reset-password-form' disabled={pending}>
            {pending && <Loader2 className='size-4 animate-spin' />}
            {title}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='reset-password-form'
          onSubmit={form.handleSubmit(submit)}
          className='space-y-4'
        >
          <FormField
            control={form.control}
            name='newPassword'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('New Password')}</FormLabel>
                <FormControl>
                  <PasswordInput
                    {...field}
                    autoComplete='new-password'
                    disabled={pending}
                  />
                </FormControl>
                <p className='text-muted-foreground text-xs'>
                  {t('Use 8–72 bytes. The user is signed out everywhere.')}
                </p>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='confirmPassword'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Confirm New Password')}</FormLabel>
                <FormControl>
                  <PasswordInput
                    {...field}
                    autoComplete='new-password'
                    disabled={pending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </form>
      </Form>
    </Dialog>
  )
}
