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
import { ExternalLink, Loader2, ShoppingBag } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useTopupInfo } from '@/features/wallet/hooks'

function getTopupStoreUrl(value: string | undefined): string | null {
  if (!value?.trim()) return null

  try {
    const url = new URL(value.trim(), window.location.origin)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
    return url.toString()
  } catch {
    return null
  }
}

export function TopupStore() {
  const { t } = useTranslation()
  const { topupInfo, loading } = useTopupInfo()
  const topupStoreUrl = getTopupStoreUrl(topupInfo?.topup_link)

  if (loading) {
    return (
      <div className='flex min-h-0 flex-1 items-center justify-center'>
        <Loader2 className='size-6 animate-spin' aria-hidden='true' />
      </div>
    )
  }

  if (!topupStoreUrl) {
    return (
      <div className='flex min-h-0 flex-1 p-4 sm:p-6'>
        <Alert className='h-fit'>
          <AlertDescription>
            {t('Top-up store link is not configured.')}
          </AlertDescription>
        </Alert>
      </div>
    )
  }

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-3 p-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex min-w-0 items-center gap-2 text-sm font-medium'>
          <ShoppingBag className='size-4 shrink-0' aria-hidden='true' />
          <span>{t('Top-up Store')}</span>
        </div>
        <Button
          variant='outline'
          size='sm'
          nativeButton={false}
          render={
            <a
              href={topupStoreUrl}
              target='_blank'
              rel='noopener noreferrer'
            />
          }
        >
          <ExternalLink className='size-4' aria-hidden='true' />
          {t('Open in new tab')}
        </Button>
      </div>
      <div className='flex min-h-0 flex-1 overflow-hidden rounded-lg border bg-background'>
        <iframe
          src={topupStoreUrl}
          title={t('Top-up Store')}
          className='h-full min-h-0 w-full border-0'
          sandbox='allow-forms allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-scripts allow-top-navigation-by-user-activation'
          allow='clipboard-read; clipboard-write; payment'
          referrerPolicy='strict-origin-when-cross-origin'
        />
      </div>
    </div>
  )
}
