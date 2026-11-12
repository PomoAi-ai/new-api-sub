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
import { useQueryClient } from '@tanstack/react-query'
import axios from 'axios'
import { Copy, Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { adminsQueryKey } from '../../admins/api'
import { createUser, usersQueryKey } from '../api'
import { randomPassword } from '../lib'
import { OwnerAdminField } from './users-mutate-drawer'

const MAX_USERS = 100
// Every try costs a bcrypt hash on the server, so skipping taken numbers is bounded.
const MAX_TAKEN = 200

const quickAddSchema = z.object({
  // 28 leaves room for the number within the 32-character username limit.
  prefix: z
    .string()
    .regex(
      /^[A-Za-z0-9_.-]{1,28}$/,
      'Prefix must be 1–28 letters, digits or _.-'
    ),
  count: z.custom<number>(
    (n) =>
      typeof n === 'number' && Number.isInteger(n) && n >= 1 && n <= MAX_USERS,
    'Quantity must be 1–100'
  ),
  admin_id: z.number().optional(),
})
type QuickAddValues = z.infer<typeof quickAddSchema>

const numbered = (prefix: string, n: number) =>
  prefix + String(n).padStart(3, '0')

type UserQuickAddDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function UserQuickAddDialog(props: UserQuickAddDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const isSuper = currentUser?.role === 'super'
  // How many are created so far; null when not creating.
  const [progress, setProgress] = useState<number | null>(null)
  // The passwords live only here and are gone once the dialog closes.
  const [created, setCreated] = useState<
    { username: string; password: string }[] | null
  >(null)
  const { copyToClipboard } = useCopyToClipboard()
  const form = useForm<QuickAddValues>({
    resolver: zodResolver(quickAddSchema),
    defaultValues: { prefix: '', count: 1 },
  })
  const values = form.watch()

  const startOver = () => {
    setCreated(null)
    form.reset({
      prefix: '',
      count: 1,
      admin_id: isSuper ? currentUser?.id : undefined,
    })
  }

  useEffect(() => {
    if (props.open) startOver()
    else setCreated(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on open and close
  }, [props.open])

  // Closing while creating would lose the passwords of the users already created.
  const onOpenChange = (open: boolean) => {
    if (progress === null) props.onOpenChange(open)
  }

  const submit = async ({ prefix, count, admin_id }: QuickAddValues) => {
    const users: { username: string; password: string }[] = []
    setProgress(0)
    try {
      // A taken number is skipped, so a second batch with the same prefix
      // continues after the first.
      for (let n = 1; users.length < count && n <= count + MAX_TAKEN; n++) {
        const username = numbered(prefix, n)
        const password = randomPassword()
        try {
          await createUser({ username, password, admin_id })
          users.push({ username, password })
          setProgress(users.length)
        } catch (error) {
          if (!axios.isAxiosError(error) || error.response?.status !== 409) {
            throw error
          }
        }
      }
      if (users.length < count) {
        toast.error(
          t('Too many numbers of this prefix are taken. Use another prefix.')
        )
      }
    } catch (error) {
      handleServerError(error, t('Failed to create user'))
    }
    setProgress(null)
    if (users.length > 0) {
      setCreated(users)
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
      void queryClient.invalidateQueries({ queryKey: adminsQueryKey })
    }
  }

  if (created) {
    return (
      <Dialog
        open={props.open}
        onOpenChange={onOpenChange}
        // Closing loses the passwords for good, so a stray click must not do it.
        disablePointerDismissal
        title={t('Created {{n}} users', { n: created.length })}
        description={t(
          'The passwords are shown only once. Copy them and send them to the users.'
        )}
        contentClassName='sm:max-w-lg'
        contentHeight='auto'
        footer={
          <>
            <Button type='button' variant='outline' onClick={startOver}>
              {t('Add More')}
            </Button>
            <Button
              type='button'
              onClick={() =>
                copyToClipboard(
                  // Tab-separated: pastes into a spreadsheet as two columns.
                  created.map((u) => `${u.username}\t${u.password}`).join('\n')
                )
              }
            >
              <Copy className='size-4' />
              {t('Copy All')}
            </Button>
          </>
        }
      >
        <div className='max-h-80 overflow-y-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Username')}</TableHead>
                <TableHead>{t('Password')}</TableHead>
                <TableHead className='w-10' />
              </TableRow>
            </TableHeader>
            <TableBody>
              {created.map((u) => (
                <TableRow key={u.username}>
                  <TableCell className='font-mono select-all'>
                    {u.username}
                  </TableCell>
                  <TableCell className='font-mono select-all'>
                    {u.password}
                  </TableCell>
                  <TableCell className='p-1'>
                    <CopyButton
                      value={`${t('Username')}: ${u.username}\n${t('Password')}: ${u.password}`}
                      className='size-7'
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </Dialog>
    )
  }

  const first = numbered(values.prefix, 1)
  const last = numbered(values.prefix, values.count)

  return (
    <Dialog
      open={props.open}
      onOpenChange={onOpenChange}
      disablePointerDismissal={progress !== null}
      title={t('Quick Add Users')}
      description={t(
        'Enter a prefix and a quantity. Usernames are numbered from 001, and every password is random.'
      )}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={progress !== null}
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='quick-add-users-form'
            disabled={progress !== null}
          >
            {progress !== null && <Loader2 className='size-4 animate-spin' />}
            {progress !== null
              ? t('Creating {{done}}/{{total}}', {
                  done: progress,
                  total: values.count,
                })
              : t('Create')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='quick-add-users-form'
          onSubmit={form.handleSubmit(submit)}
          className='space-y-4'
        >
          <div className='grid grid-cols-[1fr_7rem] items-start gap-3'>
            <FormField
              control={form.control}
              name='prefix'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Username Prefix')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      autoComplete='off'
                      placeholder={t('e.g. abc')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='count'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Quantity')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      min={1}
                      max={MAX_USERS}
                      value={Number.isNaN(field.value) ? '' : field.value}
                      onChange={(e) => field.onChange(e.target.valueAsNumber)}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
          {isSuper && <OwnerAdminField />}
          {quickAddSchema.safeParse(values).success && (
            <p className='text-muted-foreground text-sm'>
              {t('Creates {{range}}. Numbers already taken are skipped.', {
                range: first === last ? first : `${first} ~ ${last}`,
                interpolation: { escapeValue: false },
              })}
            </p>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
