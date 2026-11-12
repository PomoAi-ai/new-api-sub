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
import { cn } from '@/lib/utils'

// Required wording and link (web/ui/NOTICE, AGPLv3 Section 7(b)): do not translate or remove.
export function ProjectAttribution({ className }: { className?: string }) {
  return (
    <p
      className={cn(
        'text-muted-foreground text-[10px] leading-tight',
        className
      )}
    >
      Frontend design and development by New API contributors.{' '}
      <a
        href='https://github.com/QuantumNous/new-api'
        target='_blank'
        rel='noreferrer'
        className='hover:text-foreground underline'
      >
        new-api
      </a>
    </p>
  )
}
