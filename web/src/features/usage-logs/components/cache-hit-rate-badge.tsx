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
import { StatusBadge } from '@/components/status-badge'
import { formatPercent } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getCacheHitRateColor } from '../lib/format'

const badgeClassByColor: Record<
  ReturnType<typeof getCacheHitRateColor>,
  string
> = {
  success:
    'border-emerald-200/70 bg-emerald-50/70 !text-emerald-600 dark:border-emerald-800/60 dark:bg-emerald-950/35 dark:!text-emerald-400',
  warning:
    'border-amber-200/70 bg-amber-50/70 !text-amber-600 dark:border-amber-800/60 dark:bg-amber-950/35 dark:!text-amber-400',
  danger:
    'border-rose-200/70 bg-rose-50/70 !text-rose-600 dark:border-rose-800/60 dark:bg-rose-950/35 dark:!text-rose-400',
}

interface CacheHitRateBadgeProps {
  hitRate: number | null | undefined
  className?: string
}

export function CacheHitRateBadge(props: CacheHitRateBadgeProps) {
  if (
    props.hitRate == null ||
    !Number.isFinite(props.hitRate) ||
    props.hitRate < 0 ||
    props.hitRate > 100
  ) {
    return <span className='text-muted-foreground text-xs'>-</span>
  }

  const color = getCacheHitRateColor(props.hitRate)

  return (
    <StatusBadge
      label={formatPercent(props.hitRate)}
      variant={color}
      size='sm'
      type='badge'
      copyable={false}
      className={cn(
        'rounded-full border font-mono text-xs font-semibold tabular-nums',
        badgeClassByColor[color],
        props.className
      )}
    />
  )
}
