import { useQuery } from '@tanstack/react-query'
import { Activity, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getChannelMonitorStatus } from './api'
import type {
  ChannelMonitor,
  ChannelMonitorHistoryItem,
  ChannelMonitorStatus,
} from './types'

const STATUS_STYLES: Record<ChannelMonitorStatus, string> = {
  operational: 'bg-emerald-500',
  degraded: 'bg-amber-500',
  failed: 'bg-red-500',
  error: 'bg-slate-400',
}

const channelMonitorStatusQueryKey = ['channel-monitor', 'status'] as const
const CHANNEL_MONITOR_HISTORY_LIMIT = 18
const STATUS_REFRESH_INTERVAL_MS = 30_000

function formatAvailability(value: number) {
  return `${(Math.max(0, Math.min(1, value)) * 100).toFixed(1)}%`
}

function formatLatency(value: number) {
  if (!value || value < 0) return '-'
  return `${(value / 1000).toFixed(1)}s`
}

function formatRatio(value: number | undefined) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '-'
  return value.toFixed(4).replace(/\.?0+$/, '')
}

function statusLabel(
  status: ChannelMonitorStatus | string | undefined,
  t: (key: string) => string
) {
  switch (status) {
    case 'operational':
      return t('Operational')
    case 'degraded':
      return t('Degraded')
    case 'failed':
      return t('Failed')
    case 'error':
      return t('Monitor error')
    default:
      return t('Awaiting first check')
  }
}

function historyTitle(
  item: ChannelMonitorHistoryItem,
  t: (key: string) => string
) {
  const status = statusLabel(item.status, t)
  return `${formatTimestampToDate(item.started_at)} · ${status} · ${formatLatency(item.response_time_ms)}`
}

function useRefreshCountdown(dataUpdatedAt: number | undefined) {
  const [secondsUntilRefresh, setSecondsUntilRefresh] = useState(
    STATUS_REFRESH_INTERVAL_MS / 1000
  )

  useEffect(() => {
    const updateCountdown = () => {
      if (!dataUpdatedAt) {
        setSecondsUntilRefresh(STATUS_REFRESH_INTERVAL_MS / 1000)
        return
      }
      const elapsed = Date.now() - dataUpdatedAt
      const remaining = Math.ceil((STATUS_REFRESH_INTERVAL_MS - elapsed) / 1000)
      setSecondsUntilRefresh(Math.max(0, remaining))
    }

    updateCountdown()
    const timer = window.setInterval(updateCountdown, 1000)
    return () => window.clearInterval(timer)
  }, [dataUpdatedAt])

  return secondsUntilRefresh
}

export function HistoryTimeline(props: {
  monitor: ChannelMonitor
  refreshInSeconds: number
}) {
  const { t } = useTranslation()
  const history = props.monitor.history ?? []
  const recent = history.slice(-CHANNEL_MONITOR_HISTORY_LIMIT)
  const emptyCount = CHANNEL_MONITOR_HISTORY_LIMIT - recent.length
  const timeline = [
    ...Array.from({ length: emptyCount }, (_, index) => ({
      item: undefined,
      key: `empty-${index}`,
    })),
    ...recent.map((item, index) => ({
      item,
      key: `${item.started_at}-${item.status}-${item.response_time_ms}-${index}`,
    })),
  ]
  return (
    <div className='mt-3 min-w-0' data-slot='channel-monitor-history'>
      <div className='text-muted-foreground flex items-center justify-between gap-2 text-[11px] font-medium'>
        <span>
          {t('Last {{count}} checks', { count: CHANNEL_MONITOR_HISTORY_LIMIT })}
        </span>
        <span className='shrink-0 font-mono tabular-nums'>
          {t('Refresh in {{seconds}}s', {
            seconds: props.refreshInSeconds,
          })}
        </span>
      </div>
      <div
        className='mt-2 flex h-5 min-w-0 w-full items-stretch gap-1 overflow-hidden sm:h-6'
        role='group'
        aria-label={t('Recent checks')}
        aria-orientation='horizontal'
        data-slot='channel-monitor-history-track'
      >
        {timeline.map(({ item, key }) => {
          if (!item) {
            return (
              <span
                key={key}
                className='bg-muted-foreground/20 min-w-0 flex-1 rounded-full'
                aria-label={t('No checks yet')}
                data-slot='channel-monitor-history-segment'
                data-state='empty'
              />
            )
          }

          const title = historyTitle(item, t)
          return (
            <Tooltip key={key}>
              <TooltipTrigger
                render={
                  <span
                    className={cn(
                      'min-w-0 flex-1 rounded-full',
                      STATUS_STYLES[item.status] ?? 'bg-muted-foreground/30'
                    )}
                    aria-label={title}
                    data-slot='channel-monitor-history-segment'
                    data-state={item.status}
                  />
                }
              />
              <TooltipContent>{title}</TooltipContent>
            </Tooltip>
          )
        })}
      </div>
      <div className='text-muted-foreground mt-1 flex justify-between text-[10px] tracking-wider uppercase'>
        <span>{t('Past')}</span>
        <span>{t('Now')}</span>
      </div>
    </div>
  )
}

function MonitorCard(props: {
  monitor: ChannelMonitor
  refreshInSeconds: number
}) {
  const { t } = useTranslation()
  const monitorStatus = props.monitor.last_status as ChannelMonitorStatus
  const statusClass = STATUS_STYLES[monitorStatus] ?? 'bg-muted-foreground/40'
  return (
    <div className='border-border/70 bg-background/60 min-w-0 rounded-xl border p-3 sm:p-4'>
      <div className='flex min-w-0 items-start justify-between gap-3'>
        <div className='min-w-0'>
          <div className='flex min-w-0 items-center gap-2'>
            <span
              className={cn('size-2.5 shrink-0 rounded-full', statusClass)}
            />
            <h3 className='truncate text-sm font-semibold'>
              {props.monitor.group}
            </h3>
          </div>
          <p className='text-muted-foreground mt-1 truncate text-xs'>
            {props.monitor.model}
          </p>
          {typeof props.monitor.user_ratio === 'number' && (
            <p className='text-muted-foreground mt-1 truncate text-[11px]'>
              {t('User ratio {{ratio}}x', {
                ratio: formatRatio(props.monitor.user_ratio),
              })}
            </p>
          )}
        </div>
        <span
          className={cn(
            'shrink-0 rounded-full px-2 py-0.5 text-xs font-medium',
            monitorStatus === 'operational' &&
              'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
            monitorStatus === 'degraded' &&
              'bg-amber-500/10 text-amber-700 dark:text-amber-300',
            monitorStatus === 'failed' &&
              'bg-red-500/10 text-red-700 dark:text-red-300',
            (!monitorStatus || monitorStatus === 'error') &&
              'bg-muted text-muted-foreground'
          )}
        >
          {statusLabel(monitorStatus, t)}
        </span>
      </div>

      <div className='mt-3 grid grid-cols-3 gap-2'>
        <div className='border-border/60 rounded-lg border px-2.5 py-2'>
          <p className='text-muted-foreground text-[11px]'>
            {t('7-day uptime')}
          </p>
          <p className='mt-1 font-mono text-base font-semibold tabular-nums'>
            {formatAvailability(props.monitor.availability_7d)}
          </p>
        </div>
        <div className='border-border/60 rounded-lg border px-2.5 py-2'>
          <p className='text-muted-foreground text-[11px]'>
            {t('30-day uptime')}
          </p>
          <p className='mt-1 font-mono text-base font-semibold tabular-nums'>
            {formatAvailability(props.monitor.availability_30d)}
          </p>
        </div>
        <div className='border-border/60 rounded-lg border px-2.5 py-2'>
          <p className='text-muted-foreground text-[11px]'>{t('Latency')}</p>
          <p className='mt-1 font-mono text-base font-semibold tabular-nums'>
            {formatLatency(props.monitor.last_response_time_ms)}
          </p>
        </div>
      </div>

      <HistoryTimeline
        monitor={props.monitor}
        refreshInSeconds={props.refreshInSeconds}
      />
    </div>
  )
}

function useChannelMonitorStatusQuery() {
  return useQuery({
    queryKey: channelMonitorStatusQueryKey,
    queryFn: getChannelMonitorStatus,
    refetchInterval: STATUS_REFRESH_INTERVAL_MS,
    staleTime: 30_000,
  })
}

function StatusPageSkeleton() {
  return (
    <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-3'>
      <Skeleton className='h-52 rounded-xl' />
      <Skeleton className='h-52 rounded-xl' />
      <Skeleton className='h-52 rounded-xl' />
    </div>
  )
}

export function ChannelMonitorStatusPage() {
  const { t } = useTranslation()
  const query = useChannelMonitorStatusQuery()
  const monitors = query.data?.data?.items ?? []
  const refreshInSeconds = useRefreshCountdown(query.dataUpdatedAt)
  const hasError = query.isError || query.data?.success === false

  let content
  if (query.isLoading) {
    content = <StatusPageSkeleton />
  } else if (hasError) {
    content = (
      <div className='text-destructive flex min-h-56 items-center justify-center rounded-xl border border-dashed text-sm'>
        {t('Failed to load channel status')}
      </div>
    )
  } else if (monitors.length === 0) {
    content = (
      <div className='text-muted-foreground flex min-h-56 items-center justify-center rounded-xl border border-dashed text-sm'>
        {t('No channel status available')}
      </div>
    )
  } else {
    content = (
      <div className='space-y-3'>
        <p className='text-muted-foreground text-sm'>
          {t('Group and model checks follow routing priority and fallback')}
        </p>
        <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-3'>
          {monitors.map((monitor) => (
            <MonitorCard
              key={monitor.id}
              monitor={monitor}
              refreshInSeconds={refreshInSeconds}
            />
          ))}
        </div>
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='inline-flex min-w-0 items-center gap-2'>
          <IconBadge tone='success' size='sm'>
            <Activity />
          </IconBadge>
          <span className='truncate'>{t('Channel status')}</span>
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
          aria-label={t('Refresh')}
        >
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          <span>{t('Refresh')}</span>
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
