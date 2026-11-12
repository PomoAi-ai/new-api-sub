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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, Plus } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { TimestampCell } from '@/components/activity-time-cell'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ProofCancelled } from '@/features/account/security/api'
import { useProof } from '@/features/account/security/use-proof'
import dayjs from '@/lib/dayjs'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createApiToken,
  deleteApiToken,
  getApiTokens,
  type ApiToken,
} from './api'
import { ApiDocs } from './api-docs'

const tokensQueryKey = ['api-tokens']

function CreateTokenDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (token: string) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [expiry, setExpiry] = useState('')
  const { ask, dialog: proofDialog } = useProof()

  const close = () => {
    setName('')
    setExpiry('')
    props.onOpenChange(false)
  }
  const mutation = useMutation({
    mutationFn: async (body: Parameters<typeof createApiToken>[0]) =>
      createApiToken(body, await ask('token_create')),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: tokensQueryKey })
      close()
      props.onCreated(created.token)
    },
    onError: (error: unknown) => {
      if (error instanceof ProofCancelled) return
      handleServerError(error, t('Failed to create token'))
    },
  })
  const submit = (event: FormEvent) => {
    event.preventDefault()
    mutation.mutate({
      name: name.trim(),
      // The chosen day still counts: the token runs out as it ends.
      expires_at: expiry ? dayjs(expiry).endOf('day').unix() : undefined,
    })
  }

  return (
    <Dialog
      open={props.open}
      // The plaintext is on its way: wait for it.
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) close()
      }}
      title={t('Create Token')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={mutation.isPending}
            onClick={close}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='create-api-token-form'
            disabled={!name.trim() || mutation.isPending}
          >
            {mutation.isPending && <Loader2 className='size-4 animate-spin' />}
            {t('Create')}
          </Button>
        </>
      }
    >
      <form
        id='create-api-token-form'
        onSubmit={submit}
        className='space-y-4 p-1'
      >
        <div className='space-y-2'>
          <Label htmlFor='api-token-name'>{t('Token name')}</Label>
          <Input
            id='api-token-name'
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='api-token-expiry'>
            {t('Expiration date (optional)')}
          </Label>
          <Input
            id='api-token-expiry'
            type='date'
            min={dayjs().format('YYYY-MM-DD')}
            value={expiry}
            onChange={(e) => setExpiry(e.target.value)}
          />
        </div>
      </form>
      {proofDialog}
    </Dialog>
  )
}

function TokenTable(props: {
  tokens: ApiToken[]
  onRevoke: (token: ApiToken) => void
}) {
  const { t } = useTranslation()
  const time = (timestamp: number) => (
    <TimestampCell
      timestamp={timestamp}
      format='absolute'
      justNowLabel={t('Just now')}
      className='text-muted-foreground text-xs'
    />
  )

  return (
    <div className='overflow-hidden rounded-lg border'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Name')}</TableHead>
            <TableHead>{t('Token')}</TableHead>
            <TableHead>{t('Expiration')}</TableHead>
            <TableHead>{t('Last Used')}</TableHead>
            <TableHead>{t('Created At')}</TableHead>
            <TableHead className='text-end'>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.tokens.map((token) => (
            <TableRow key={token.id}>
              <TableCell className='max-w-64 truncate'>{token.name}</TableCell>
              <TableCell className='font-mono text-xs'>{token.hint}</TableCell>
              <TableCell>
                {token.expires_at ? time(token.expires_at) : t('Never')}
              </TableCell>
              <TableCell>{time(token.last_used_at ?? 0)}</TableCell>
              <TableCell>{time(token.created_at)}</TableCell>
              <TableCell className='text-end'>
                <Button
                  variant='ghost'
                  size='sm'
                  className='text-destructive'
                  onClick={() => props.onRevoke(token)}
                >
                  {t('Revoke')}
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

/** The signed-in user's tokens for calling the portal from a program. */
export function ApiTokens() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [tab, setTab] = useState('tokens')
  const [creating, setCreating] = useState(false)
  const [plaintext, setPlaintext] = useState<string | null>(null)
  const [revoking, setRevoking] = useState<ApiToken | null>(null)

  const query = useQuery({
    queryKey: tokensQueryKey,
    queryFn: getApiTokens,
    retry: 1,
    meta: { errorToast: false, errorRedirect: false },
  })
  const revoke = useMutation({
    mutationFn: deleteApiToken,
    onSuccess: () => {
      toast.success(t('Token revoked'))
    },
    onError: (error: unknown) => {
      handleServerError(error, t('Failed to revoke token'))
    },
    // Also after a failure: a 404 means it is already gone from another tab.
    onSettled: () => {
      setRevoking(null)
      void queryClient.invalidateQueries({ queryKey: tokensQueryKey })
    },
  })

  let content = <LoadingState message={t('Loading...')} />
  if (query.isError) {
    content = (
      <ErrorState
        title={t('Failed to load API tokens')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data?.length === 0) {
    content = (
      <EmptyState
        className='min-h-0 rounded-lg border p-4'
        title={t('No API tokens yet')}
      />
    )
  } else if (query.data) {
    content = <TokenTable tokens={query.data} onRevoke={setRevoking} />
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('API Tokens')}</SectionPageLayout.Title>
        {tab === 'tokens' && (
          <SectionPageLayout.Actions>
            <Button size='sm' onClick={() => setCreating(true)}>
              <Plus className='h-4 w-4' />
              {t('Create Token')}
            </Button>
          </SectionPageLayout.Actions>
        )}
        <SectionPageLayout.Content>
          <Tabs value={tab} onValueChange={setTab} className='gap-4'>
            <TabsList>
              <TabsTrigger value='tokens'>{t('Token List')}</TabsTrigger>
              <TabsTrigger value='docs'>{t('API Docs')}</TabsTrigger>
            </TabsList>
            <TabsContent value='tokens' className='space-y-4'>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Tokens are for programs and carry the same permissions as your actions on this site. Do not share them. Changing your password revokes all of them.'
                )}
              </p>
              {content}
            </TabsContent>
            <TabsContent value='docs'>
              <ApiDocs />
            </TabsContent>
          </Tabs>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <CreateTokenDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={setPlaintext}
      />

      <Dialog
        open={plaintext !== null}
        // The only place the token can be seen: Escape, a click outside and
        // the corner button all do nothing, only the button below closes it.
        onOpenChange={() => {}}
        showCloseButton={false}
        title={t('Token created')}
        description={t(
          'Copy this token now. Once this dialog is closed it cannot be viewed again.'
        )}
        contentClassName='sm:max-w-lg'
        contentHeight='auto'
        footer={
          <Button type='button' onClick={() => setPlaintext(null)}>
            {t('I have saved it')}
          </Button>
        }
      >
        <div className='flex items-start gap-2 rounded-lg border p-3'>
          <pre className='min-w-0 flex-1 font-mono text-sm break-all whitespace-pre-wrap select-all'>
            {plaintext}
          </pre>
          <CopyButton value={plaintext ?? ''} className='size-7' />
        </div>
      </Dialog>

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open && !revoke.isPending) setRevoking(null)
        }}
        title={t('Revoke token "{{name}}"?', { name: revoking?.name ?? '' })}
        desc={t('Programs using it stop working immediately.')}
        confirmText={t('Revoke')}
        destructive
        isLoading={revoke.isPending}
        handleConfirm={() => {
          if (revoking) revoke.mutate(revoking.id)
        }}
      />
    </>
  )
}
