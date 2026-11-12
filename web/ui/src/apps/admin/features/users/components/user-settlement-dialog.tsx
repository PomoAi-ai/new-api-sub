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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { SettlementOverview } from '@/features/settlements/components/settlement-overview'
import { handleServerError } from '@/lib/handle-server-error'

import {
  addUserSettlement,
  getUserSettlements,
  userSettlementsQueryKey,
  usersQueryKey,
  type PortalUser,
} from '../api'

const settlementSchema = z.object({
  amount_usd: z.custom<number>(
    (n) => typeof n === 'number' && Number.isFinite(n) && n >= 0,
    'Enter an amount of 0 or more'
  ),
  note: z.string().trim(),
})
type SettlementValues = z.infer<typeof settlementSchema>

type UserSettlementDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: PortalUser | null
}

export function UserSettlementDialog(props: UserSettlementDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { open, user } = props
  const form = useForm<SettlementValues>({
    resolver: zodResolver(settlementSchema),
    defaultValues: { amount_usd: Number.NaN, note: '' },
  })

  useEffect(() => {
    if (open) form.reset({ amount_usd: Number.NaN, note: '' })
  }, [open, form])

  const userId = user?.id ?? 0
  const query = useQuery({
    queryKey: userSettlementsQueryKey(userId),
    queryFn: () => getUserSettlements(userId),
    enabled: open && userId > 0,
    retry: 1,
    meta: { errorToast: false, errorRedirect: false },
  })

  const mutation = useMutation({
    mutationFn: (values: SettlementValues) => addUserSettlement(userId, values),
    onSuccess: () => {
      toast.success(t('Settlement recorded'))
      form.reset({ amount_usd: Number.NaN, note: '' })
      void queryClient.invalidateQueries({
        queryKey: userSettlementsQueryKey(userId),
      })
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
    },
    onError: (error: unknown) => {
      handleServerError(error, t('Failed to record settlement'))
    },
  })

  let overview = <LoadingState message={t('Loading...')} />
  if (query.isError) {
    overview = (
      <ErrorState
        className='min-h-0 p-3'
        title={t('Failed to load settlement records')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data) {
    overview = <SettlementOverview overview={query.data} showRecordedBy />
  }

  return (
    <Dialog
      open={open}
      onOpenChange={props.onOpenChange}
      title={t('Settlement')}
      description={user?.username}
      contentClassName='sm:max-w-2xl'
      contentHeight='auto'
    >
      <div className='space-y-5'>
        <Form {...form}>
          <form
            onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
            className='space-y-4 rounded-lg border p-4'
          >
            <FormField
              control={form.control}
              name='amount_usd'
              render={({ field }) => (
                <FormItem>
                  <FormLabel required>{t('Settled up to (USD)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step='0.01'
                      value={Number.isNaN(field.value) ? '' : field.value}
                      onChange={(e) => field.onChange(e.target.valueAsNumber)}
                      onBlur={field.onBlur}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'The running total settled so far, not this payment: 10000 means settled up to 10,000 USD. Records cannot be deleted.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='note'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Note')}</FormLabel>
                  <FormControl>
                    <Input {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='flex justify-end'>
              <Button type='submit' disabled={mutation.isPending}>
                {mutation.isPending && (
                  <Loader2 className='size-4 animate-spin' />
                )}
                {t('Record Settlement')}
              </Button>
            </div>
          </form>
        </Form>
        {overview}
      </div>
    </Dialog>
  )
}
