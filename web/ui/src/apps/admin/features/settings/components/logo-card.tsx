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
import { useQueryClient } from '@tanstack/react-query'
import { Upload } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { useSystemConfig } from '@/hooks/use-system-config'
import { DEFAULT_LOGO } from '@/lib/constants'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { deleteLogo, uploadLogo } from '../api'

const MAX_LOGO_BYTES = 256 * 1024
const ICO_TYPE = 'image/x-icon'
const LOGO_TYPES = new Set([
  'image/png',
  'image/jpeg',
  'image/webp',
  ICO_TYPE,
  'image/vnd.microsoft.icon',
])

/** Browsers disagree on .ico: some report no type at all. */
function logoContentType(file: File): string | null {
  if (LOGO_TYPES.has(file.type)) return file.type
  return !file.type && file.name.toLowerCase().endsWith('.ico')
    ? ICO_TYPE
    : null
}

export function LogoCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { logo } = useSystemConfig()
  const inputRef = useRef<HTMLInputElement>(null)
  const [pending, setPending] = useState(false)

  const run = async (action: () => Promise<void>, success: string) => {
    setPending(true)
    try {
      await action()
      toast.success(success)
      void queryClient.invalidateQueries({
        queryKey: STATUS_QUERY_KEY,
        refetchType: 'all',
      })
    } catch (error) {
      handleServerError(error, t('Failed to update the logo'))
    } finally {
      setPending(false)
    }
  }

  const handleFile = (file: File) => {
    const contentType = logoContentType(file)
    if (!contentType) {
      toast.error(t('The logo must be a PNG, JPEG, WebP or ICO image.'))
      return
    }
    if (file.size > MAX_LOGO_BYTES) {
      toast.error(t('The logo must be 256 KB or smaller.'))
      return
    }
    void run(() => uploadLogo(file, contentType), t('Logo updated'))
  }

  return (
    <div className='flex flex-wrap items-center gap-4'>
      <img
        src={logo}
        alt={t('Logo')}
        className='size-14 rounded-lg border object-cover'
      />
      <div className='space-y-2'>
        <div className='flex flex-wrap gap-2'>
          <input
            ref={inputRef}
            type='file'
            hidden
            accept='.png,.jpg,.jpeg,.webp,.ico,image/png,image/jpeg,image/webp,image/x-icon'
            onChange={(event) => {
              const file = event.target.files?.[0]
              // Allows picking the same file again.
              event.target.value = ''
              if (file) handleFile(file)
            }}
          />
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={pending}
            onClick={() => inputRef.current?.click()}
          >
            <Upload className='size-4' />
            {t('Upload Logo')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={pending || logo === DEFAULT_LOGO}
            onClick={() =>
              void run(deleteLogo, t('Logo restored to the default'))
            }
          >
            {t('Restore Default')}
          </Button>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'PNG, JPEG, WebP or ICO, up to 256 KB. Also used as the browser tab icon.'
          )}
        </p>
      </div>
    </div>
  )
}
