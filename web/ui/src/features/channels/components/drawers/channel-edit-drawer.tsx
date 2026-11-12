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
import { KeyRound, Loader2, Server } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { MultiSelect } from '@/components/multi-select'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { IconBadge } from '@/components/ui/icon-badge'
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

import { getChannelTypes, updateChannel } from '../../api'
import {
  CHANNEL_TYPE_OPTIONS,
  ERROR_MESSAGES,
  FIELD_DESCRIPTIONS,
  FIELD_PLACEHOLDERS,
} from '../../constants'
import {
  channelsQueryKeys,
  isMultiKeyChannel,
  parseModelsString,
} from '../../lib'
import type { Channel } from '../../types'
import {
  ChannelKeysDialog,
  ChannelKeysPanel,
} from '../dialogs/channel-keys-dialog'
import { ChannelBasicSection, ChannelModelsSection } from './sections'

const channelEditSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, ERROR_MESSAGES.REQUIRED_NAME)
    .max(40, 'Name must be at most 40 characters'),
  models: z.array(z.string()).min(1, ERROR_MESSAGES.REQUIRED_MODELS),
})
type ChannelEditValues = z.infer<typeof channelEditSchema>

type ChannelEditDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channel: Channel | null
}

/** Only the name and the models can change; the key is never shown. */
export function ChannelEditDrawer(props: ChannelEditDrawerProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { open, channel } = props
  const [keysOpen, setKeysOpen] = useState(false)
  const form = useForm<ChannelEditValues>({
    resolver: zodResolver(channelEditSchema),
    defaultValues: { name: '', models: [] },
  })

  useEffect(() => {
    if (open && channel) {
      form.reset({
        name: channel.name,
        models: parseModelsString(channel.models),
      })
    }
  }, [open, channel, form])

  const typesQuery = useQuery({
    queryKey: channelsQueryKeys.types(),
    queryFn: getChannelTypes,
    enabled: open,
  })
  const sameType = typesQuery.data?.filter(
    (item) => item.type === channel?.type
  )
  // A retired template leaves only the type: that decides when nothing else can.
  const templateModels = (
    sameType?.find((item) => item.template_id === channel?.template_id) ??
    (sameType?.length === 1 ? sameType[0] : undefined)
  )?.models
  // The channel's own models stay listed: the template may have dropped them
  // since, and someone who only renames must not be forced to change them.
  const modelOptions = useMemo(
    () =>
      [
        ...new Set([
          ...parseModelsString(templateModels ?? ''),
          ...parseModelsString(channel?.models ?? ''),
        ]),
      ].map((model) => ({ value: model, label: model })),
    [templateModels, channel?.models]
  )
  const modelsLocked = templateModels === undefined

  const mutation = useMutation({
    mutationFn: (values: ChannelEditValues) => {
      if (!channel) throw new Error('No channel to edit')
      return updateChannel(channel.id, {
        name: values.name,
        models: values.models.join(','),
      })
    },
    onSuccess: () => {
      toast.success(t('Channel updated successfully'))
      void queryClient.invalidateQueries({
        queryKey: channelsQueryKeys.lists(),
      })
      props.onOpenChange(false)
    },
    onError: (error: unknown) => {
      handleServerError(error, t('Failed to update channel'))
    },
  })

  const typeLabel = CHANNEL_TYPE_OPTIONS.find(
    (option) => option.value === channel?.type
  )?.label

  const readOnlyField = (
    label: string,
    value: string,
    placeholder?: string
  ) => (
    <FormItem>
      <FormLabel>{label}</FormLabel>
      <FormControl>
        <Input value={value} placeholder={placeholder} disabled readOnly />
      </FormControl>
    </FormItem>
  )

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (next || !mutation.isPending) props.onOpenChange(next)
      }}
    >
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-3xl')}
      >
        <SheetHeader className={sideDrawerHeaderClassName('pr-12 sm:pr-14')}>
          <SheetTitle className='flex items-center gap-2 sm:gap-3'>
            <IconBadge tone='info' size='title'>
              <Server className='size-5' />
            </IconBadge>
            <span>{t('Edit Channel')}</span>
          </SheetTitle>
          <SheetDescription>
            {t(
              'Only the name and the models can be changed. The key, the address and everything else stay as they are.'
            )}
          </SheetDescription>
        </SheetHeader>

        <Form {...form}>
          <form
            id='channel-edit-form'
            onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
            className={sideDrawerFormClassName('gap-5')}
          >
            <ChannelBasicSection>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel required>{t('Name')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t(FIELD_PLACEHOLDERS.NAME)}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {readOnlyField(
                t('Channel Type'),
                typeLabel ? t(typeLabel) : `#${channel?.type ?? ''}`
              )}
              {readOnlyField(
                t('Base URL'),
                channel?.base_url ?? '',
                t('Default address of the channel type')
              )}
            </ChannelBasicSection>

            <ChannelModelsSection>
              {modelsLocked && !typesQuery.isLoading && (
                <Alert>
                  <AlertDescription>
                    {t(
                      'The template of this channel is no longer enabled, so only the name can be changed.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              <FormField
                control={form.control}
                name='models'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel required>{t('Models')}</FormLabel>
                    <FormControl>
                      <MultiSelect
                        options={modelOptions}
                        selected={field.value}
                        onChange={field.onChange}
                        placeholder={t('Select models')}
                        maxVisibleChips={8}
                        copyChipOnClick
                        disabled={modelsLocked}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(FIELD_DESCRIPTIONS.MODELS)}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {readOnlyField(t('Groups'), channel?.group ?? '')}
              <div className='grid gap-4 sm:grid-cols-2'>
                {readOnlyField(t('Priority'), String(channel?.priority ?? 0))}
                {readOnlyField(t('Weight'), String(channel?.weight ?? 0))}
              </div>
            </ChannelModelsSection>

            {channel && isMultiKeyChannel(channel) && (
              <SideDrawerSection>
                <div className='flex items-start justify-between gap-3'>
                  <SideDrawerSectionHeader
                    title={t('Keys')}
                    description={t(
                      'Only the key numbers are shown; the keys themselves stay hidden.'
                    )}
                    icon={<KeyRound className='h-4 w-4' aria-hidden='true' />}
                    iconTone='warning'
                  />
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() => setKeysOpen(true)}
                  >
                    {t('Manage Keys')}
                  </Button>
                </div>
                <ChannelKeysPanel channel={channel} variant='readonly' />
              </SideDrawerSection>
            )}
          </form>
        </Form>

        <ChannelKeysDialog
          channel={channel}
          open={keysOpen}
          onOpenChange={setKeysOpen}
        />

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose
            render={<Button variant='outline' disabled={mutation.isPending} />}
          >
            {t('Cancel')}
          </SheetClose>
          <Button
            form='channel-edit-form'
            type='submit'
            disabled={mutation.isPending}
          >
            {mutation.isPending && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            )}
            {t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
