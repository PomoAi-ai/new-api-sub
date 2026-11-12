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
// Modified 2026-11-04 by new-api-sub: create-only portal user drawer (username, password, owner admin for super)
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { ADMIN_STATUS, adminsQueryKey, getAdmins } from '../../admins/api'
import { createUser, usersQueryKey } from '../api'
import { passwordSchema, usernameSchema } from '../lib'

const userFormSchema = z.object({
  username: usernameSchema,
  password: passwordSchema,
  admin_id: z.number().optional(),
})
type UserFormValues = z.infer<typeof userFormSchema>
const USER_FORM_DEFAULT_VALUES: UserFormValues = { username: '', password: '' }

/** Super only: the admin who owns the new user. Reads the surrounding <Form>. */
export function OwnerAdminField() {
  const { t } = useTranslation()
  const { data: admins } = useQuery({
    queryKey: adminsQueryKey,
    queryFn: getAdmins,
  })

  return (
    <FormField
      name='admin_id'
      render={({ field }) => (
        <FormItem>
          <FormLabel>{t('Owner Admin')}</FormLabel>
          <FormControl>
            <Combobox
              options={(admins ?? [])
                .filter((admin) => admin.status === ADMIN_STATUS.ENABLED)
                .map((admin) => ({
                  value: String(admin.id),
                  label: admin.username,
                }))}
              value={String(field.value ?? '')}
              onValueChange={(value) =>
                value !== null && field.onChange(Number(value))
              }
              className='w-full'
              placeholder={t('Select an admin')}
            />
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

type UsersMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function UsersMutateDrawer({
  open,
  onOpenChange,
}: UsersMutateDrawerProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const currentUser = useAuthStore((s) => s.auth.user)
  const isSuper = currentUser?.role === 'super'
  const currentUserId = currentUser?.id

  const form = useForm<UserFormValues>({
    resolver: zodResolver(userFormSchema),
    defaultValues: USER_FORM_DEFAULT_VALUES,
  })

  useEffect(() => {
    if (!open) return
    form.reset({
      ...USER_FORM_DEFAULT_VALUES,
      admin_id: isSuper ? currentUserId : undefined,
    })
  }, [open, form, isSuper, currentUserId])

  const onSubmit = async (data: UserFormValues) => {
    setIsSubmitting(true)
    try {
      await createUser(data)
      toast.success(t('User created successfully'))
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
      void queryClient.invalidateQueries({ queryKey: adminsQueryKey })
    } catch (error) {
      handleServerError(error, t('Failed to create user'))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[600px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {t('Create')} {t('User')}
          </SheetTitle>
          <SheetDescription>
            {t('Add a new user by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='user-form'
            onSubmit={form.handleSubmit(onSubmit)}
            className={sideDrawerFormClassName()}
          >
            <SideDrawerSection>
              <h3 className='text-sm font-medium'>{t('Basic Information')}</h3>

              <FormField
                control={form.control}
                name='username'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Username')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('Enter username')}
                        autoComplete='off'
                      />
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
                    <FormLabel>{t('Password')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='password'
                        autoComplete='new-password'
                        placeholder={t('Enter password (8–72 bytes)')}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {isSuper && <OwnerAdminField />}
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button form='user-form' type='submit' disabled={isSubmitting}>
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
