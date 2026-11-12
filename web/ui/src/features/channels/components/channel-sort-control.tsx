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
// Modified 2026-11-04 by new-api-sub: ChannelSortControl from channel-priority-controls.tsx (no drag reordering)
import { ArrowDown, ArrowUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ButtonGroup } from '@/components/ui/button-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { ChannelSortBy } from '../types'

export function ChannelSortControl(props: {
  field: ChannelSortBy
  desc: boolean
  disabled?: boolean
  onChange: (field: ChannelSortBy, desc: boolean) => void
}) {
  const { t } = useTranslation()
  const options = [
    { value: 'priority', label: t('Priority') },
    { value: 'id', label: t('ID') },
    { value: 'name', label: t('Name') },
    { value: 'balance', label: t('Balance') },
    { value: 'response_time', label: t('Response') },
    { value: 'test_time', label: t('Last Tested') },
  ]
  return (
    <ButtonGroup aria-label={t('Sort channels by')} className='shrink-0'>
      <Select
        value={props.field}
        disabled={props.disabled}
        onValueChange={(value) => {
          if (value) props.onChange(value as ChannelSortBy, props.desc)
        }}
        items={options}
      >
        <SelectTrigger
          aria-label={t('Sort channels by')}
          className='bg-background min-w-24'
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button
        type='button'
        size='icon'
        variant='outline'
        disabled={props.disabled}
        aria-label={props.desc ? t('Descending') : t('Ascending')}
        title={props.desc ? t('Descending') : t('Ascending')}
        onClick={() => props.onChange(props.field, !props.desc)}
      >
        {props.desc ? (
          <ArrowDown aria-hidden className='size-4' />
        ) : (
          <ArrowUp aria-hidden className='size-4' />
        )}
      </Button>
    </ButtonGroup>
  )
}
