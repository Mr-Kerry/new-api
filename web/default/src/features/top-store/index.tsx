import { ExternalLink, Loader2, ShoppingCart } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { useTopupInfo } from '@/features/wallet/hooks'

export function TopupStore() {
  const { t } = useTranslation()
  const { topupInfo, loading } = useTopupInfo()

  const topupLink = topupInfo?.topup_link?.trim()

  if (loading) {
    return (
      <div className='flex h-[calc(100vh-4rem)] items-center justify-center'>
        <Loader2 className='h-6 w-6 animate-spin' />
      </div>
    )
  }

  if (!topupLink) {
    return (
      <div className='p-6'>
        <Alert>
          <AlertDescription>
            {t('Top-up store link is not configured.')}
          </AlertDescription>
        </Alert>
      </div>
    )
  }

  return (
    <div className='flex h-[calc(100vh-4rem)] flex-col gap-3 p-3'>
      <div className='flex items-center justify-between gap-3'>
        <div className='flex items-center gap-2 text-sm font-medium'>
          <ShoppingCart className='h-4 w-4' />
          {t('Top-up Store')}
        </div>

        <Button variant='outline' size='sm' asChild>
          <a href={topupLink} target='_blank' rel='noopener noreferrer'>
            <ExternalLink className='mr-2 h-4 w-4' />
            {t('Open in new tab')}
          </a>
        </Button>
      </div>

      <div className='min-h-0 flex-1 overflow-hidden rounded-lg border bg-background'>
        <iframe
          src={topupLink}
          title='Top-up Store'
          className='h-full w-full border-0'
          allow='clipboard-read; clipboard-write; payment'
          referrerPolicy='strict-origin-when-cross-origin'
        />
      </div>
    </div>
  )
}