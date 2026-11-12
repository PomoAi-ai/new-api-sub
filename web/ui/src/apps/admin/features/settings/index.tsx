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

import { SideDrawerSection } from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  SettingsFormGrid,
  SettingsSwitchField,
} from '@/features/system-settings/components/settings-form-layout'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import {
  getSettings,
  settingsQueryKey,
  updateSettings,
  type AppSettings,
} from './api'
import { LogoCard } from './components/logo-card'
import { PoolsCard } from './components/pools-card'

const settingsSchema = z.object({
  portal_name: z.string().trim().min(1, 'Name is required').max(64),
  admin_name: z.string().trim().min(1, 'Name is required').max(64),
  log_scope: z.enum(['all', 'self']),
  portal_public_url: z
    .string()
    .trim()
    .refine(
      (v) => v === '' || /^https?:\/\/[^/\s]+/.test(v),
      'The login URL must be a full http(s) address'
    ),
  key_probe: z.boolean(),
})

function SettingsForm({ settings }: { settings: AppSettings }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<AppSettings>({
    resolver: zodResolver(settingsSchema),
    defaultValues: settings,
  })

  useEffect(() => {
    form.reset(settings)
  }, [settings, form])

  const mutation = useMutation({
    mutationFn: updateSettings,
    onSuccess: (saved) => {
      toast.success(t('Settings saved'))
      queryClient.setQueryData(settingsQueryKey, saved)
      void queryClient.invalidateQueries({
        queryKey: STATUS_QUERY_KEY,
        refetchType: 'all',
      })
      // The copy buttons on the users page read this.
      const { user, setUser } = useAuthStore.getState().auth
      if (user) setUser({ ...user, portal_url: saved.portal_public_url })
    },
    onError: (error: unknown) => {
      handleServerError(error, t('Failed to save settings'))
    },
  })

  const logScopeItems = [
    { value: 'all', label: t('All logs') },
    { value: 'self', label: t('Own channels only') },
  ]

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
        className='space-y-5'
      >
        <SettingsFormGrid>
          <FormField
            control={form.control}
            name='portal_name'
            render={({ field }) => (
              <FormItem>
                <FormLabel required>{t('Portal Name')}</FormLabel>
                <FormControl>
                  <Input {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='admin_name'
            render={({ field }) => (
              <FormItem>
                <FormLabel required>{t('Admin Console Name')}</FormLabel>
                <FormControl>
                  <Input {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='log_scope'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Global Log Scope')}</FormLabel>
                <Select
                  items={logScopeItems}
                  value={field.value}
                  onValueChange={field.onChange}
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {logScopeItems.map((item) => (
                        <SelectItem key={item.value} value={item.value}>
                          {item.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'What portal users see in logs. A user can be set differently on the Users page.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='portal_public_url'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Portal Login URL')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='https://portal.example.com'
                    className='font-mono'
                  />
                </FormControl>
                <FormDescription>
                  {t('Included when copying an account on the Users page.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='key_probe'
            render={({ field }) => (
              <SettingsSwitchField
                controlId='settings-key-probe'
                checked={field.value}
                onCheckedChange={field.onChange}
                label={t('Test and query the balance of each key')}
                description={t(
                  'When on, a test makes one real call with the selected model, directly to OpenRouter, without going through new-api or into the usage logs. Balances are also queried directly. Keys are never sent to the browser.'
                )}
              />
            )}
          />
        </SettingsFormGrid>
        <Button type='submit' disabled={mutation.isPending}>
          {mutation.isPending && <Loader2 className='size-4 animate-spin' />}
          {t('Save changes')}
        </Button>
      </form>
    </Form>
  )
}

export function Settings() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: settingsQueryKey,
    queryFn: getSettings,
  })

  let form = <LoadingState message={t('Loading...')} />
  if (query.isError) {
    form = (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data) {
    form = <SettingsForm settings={query.data} />
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('System Settings')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='max-w-4xl space-y-5'>
          <SideDrawerSection>
            <h3 className='text-sm font-medium'>{t('Logo')}</h3>
            <LogoCard />
          </SideDrawerSection>
          <SideDrawerSection>
            <h3 className='text-sm font-medium'>{t('General')}</h3>
            {form}
          </SideDrawerSection>
          <SideDrawerSection>
            <h3 className='text-sm font-medium'>{t('new-api Pools')}</h3>
            <PoolsCard />
          </SideDrawerSection>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
