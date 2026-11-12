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
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { usernameSchema } from '../users/lib'

const schema = z.object({
  username: usernameSchema,
  password: z.string().min(1, 'Please enter your current password'),
})
type Values = z.infer<typeof schema>

export function ChangeUsernameDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const { user, setUser } = useAuthStore((s) => s.auth)
  const [pending, setPending] = useState(false)
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })

  useEffect(() => {
    if (props.open) form.reset({ username: user?.username ?? '', password: '' })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on open
  }, [props.open])

  const submit = async (values: Values) => {
    setPending(true)
    try {
      await api.post('/api/me/username', values)
      if (user) setUser({ ...user, username: values.username })
      toast.success(t('Account changed successfully'))
      props.onOpenChange(false)
    } catch (error) {
      handleServerError(error, t('Failed to change account'))
    } finally {
      setPending(false)
    }
  }
  const title = t('Change Account')

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={title}
      description={t('Sign in with the new username from now on.')}
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
          <Button type='submit' form='change-username-form' disabled={pending}>
            {pending && <Loader2 className='size-4 animate-spin' />}
            {title}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='change-username-form'
          onSubmit={form.handleSubmit(submit)}
          className='space-y-4'
        >
          <FormField
            control={form.control}
            name='username'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Username')}</FormLabel>
                <FormControl>
                  <Input {...field} autoComplete='off' />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='password'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Current Password')}</FormLabel>
                <FormControl>
                  <PasswordInput {...field} autoComplete='current-password' />
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
