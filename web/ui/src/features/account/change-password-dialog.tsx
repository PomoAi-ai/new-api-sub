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
// Modified 2026-11-04 by new-api-sub: own password change on either system (from features/security/components/dialogs/change-password-dialog.tsx); no step-up verification, POST /api/me/password, 8–72 byte rule
import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
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
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

// bcrypt on the backend limits passwords to 8–72 bytes.
const passwordSchema = z.string().refine((password) => {
  const bytes = new TextEncoder().encode(password).length
  return bytes >= 8 && bytes <= 72
}, 'Password must be 8–72 bytes.')

const passwordChangeSchema = z
  .object({
    originalPassword: z.string().min(1, 'Please enter your current password'),
    newPassword: passwordSchema,
    confirmPassword: z.string(),
  })
  .refine((data) => data.newPassword === data.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })
  .refine((data) => data.originalPassword !== data.newPassword, {
    message: 'New password must be different from current password',
    path: ['newPassword'],
  })
type PasswordChangeValues = z.infer<typeof passwordChangeSchema>
const emptyPasswordForm: PasswordChangeValues = {
  originalPassword: '',
  newPassword: '',
  confirmPassword: '',
}

interface ChangePasswordDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ChangePasswordDialog(props: ChangePasswordDialogProps) {
  const { t } = useTranslation()
  const username = useAuthStore((s) => s.auth.user?.username)
  const [pending, setPending] = useState(false)
  const form = useForm<PasswordChangeValues>({
    resolver: zodResolver(passwordChangeSchema),
    defaultValues: emptyPasswordForm,
  })

  const handleOpenChange = (open: boolean) => {
    if (!open) form.reset(emptyPasswordForm)
    props.onOpenChange(open)
  }
  // The backend re-issues the session cookie (204), so the user stays signed in.
  const submit = async (values: PasswordChangeValues) => {
    setPending(true)
    try {
      await api.post('/api/me/password', {
        old_password: values.originalPassword,
        new_password: values.newPassword,
      })
      toast.success(t('Password changed successfully'))
      handleOpenChange(false)
    } catch (error) {
      handleServerError(error, t('Failed to change password'))
    } finally {
      setPending(false)
    }
  }
  const title = t('Change Password')

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleOpenChange}
      title={title}
      description={
        <>
          {t('Update your password for account:')} <strong>{username}</strong>
        </>
      }
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => handleOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form='change-password-form' disabled={pending}>
            {pending && <Loader2 className='size-4 animate-spin' />}
            {title}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='change-password-form'
          onSubmit={form.handleSubmit(submit)}
          className='space-y-4'
        >
          <FormField
            control={form.control}
            name='originalPassword'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Current Password')}</FormLabel>
                <FormControl>
                  <PasswordInput
                    {...field}
                    autoComplete='current-password'
                    disabled={pending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
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
                  {t('Use 8–72 bytes.')}
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
