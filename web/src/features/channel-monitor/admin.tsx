import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Eye, Plus, RefreshCw, RotateCw, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { CHANNEL_TEST_ENDPOINT_TYPE_OPTIONS } from '@/features/channels/constants'
import { getGroups } from '@/features/users/api'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  createChannelMonitor,
  deleteChannelMonitor,
  getChannelMonitorModels,
  getChannelMonitorRuns,
  getChannelMonitorTask,
  getChannelMonitors,
  runChannelMonitor,
  updateChannelMonitor,
} from './api'
import type {
  ChannelMonitor,
  ChannelMonitorInput,
  ChannelMonitorRun,
  ChannelMonitorStatus,
} from './types'

const monitorQueryKey = ['channel-monitor', 'admin'] as const
const CHANNEL_MONITOR_HISTORY_LIMIT = 18
const CHANNEL_MONITOR_TASK_POLL_INTERVAL_MS = 1_000

const STATUS_STYLES: Record<ChannelMonitorStatus, string> = {
  operational: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  degraded: 'bg-amber-500/10 text-amber-700 dark:text-amber-300',
  failed: 'bg-red-500/10 text-red-700 dark:text-red-300',
  error: 'bg-muted text-muted-foreground',
}

function formatAvailability(value: number) {
  return `${(Math.max(0, Math.min(1, value)) * 100).toFixed(1)}%`
}

function formatLatency(value: number) {
  return value > 0 ? `${(value / 1000).toFixed(1)}s` : '-'
}

function getStatusLabel(
  status: string | undefined,
  t: (key: string) => string
) {
  if (status === 'operational') return t('Operational')
  if (status === 'degraded') return t('Degraded')
  if (status === 'failed') return t('Failed')
  if (status === 'error') return t('Monitor error')
  return t('Awaiting first check')
}

function MonitorForm(props: {
  initial?: ChannelMonitor
  submitting: boolean
  onSubmit: (input: ChannelMonitorInput) => void
}) {
  const { t } = useTranslation()
  const targetLocked = props.initial !== undefined
  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: ['groups'],
    queryFn: getGroups,
    enabled: !targetLocked,
    staleTime: 60_000,
  })
  const [group, setGroup] = useState(props.initial?.group ?? '')
  const [model, setModel] = useState(props.initial?.model ?? '')
  const normalizedGroup = group.trim()
  const modelsQuery = useQuery({
    queryKey: ['channel-monitor', 'models', normalizedGroup],
    queryFn: () => getChannelMonitorModels(normalizedGroup),
    enabled: !targetLocked && normalizedGroup !== '',
    staleTime: 60_000,
  })
  const [endpointType, setEndpointType] = useState(
    props.initial?.endpoint_type || 'auto'
  )
  const [interval, setInterval] = useState(
    String(props.initial?.interval_seconds ?? 1800)
  )
  const [timeout, setTimeout] = useState(
    String(props.initial?.timeout_ms ?? 15000)
  )
  const [scheduleStart, setScheduleStart] = useState(
    props.initial?.schedule_start_time ?? ''
  )
  const [scheduleEnd, setScheduleEnd] = useState(
    props.initial?.schedule_end_time ?? ''
  )
  const [enabled, setEnabled] = useState(props.initial?.enabled ?? true)
  const groupOptions = useMemo(() => {
    const values = new Set(
      (groupsData?.data ?? [])
        .filter(
          (value): value is string =>
            typeof value === 'string' && value.trim().toLowerCase() !== 'auto'
        )
        .map((value) => value.trim())
    )
    const currentGroup = props.initial?.group?.trim()
    if (currentGroup && currentGroup.toLowerCase() !== 'auto') {
      values.add(currentGroup)
    }
    return [...values]
      .sort((left, right) => left.localeCompare(right))
      .map((value) => ({ value, label: value }))
  }, [groupsData?.data, props.initial?.group])
  const modelOptions = useMemo(() => {
    const values = new Set(
      (modelsQuery.data?.data?.items ?? []).filter(
        (value): value is string => typeof value === 'string' && value !== ''
      )
    )
    const initialModel = props.initial?.model?.trim()
    if (props.initial?.group === normalizedGroup && initialModel) {
      values.add(initialModel)
    }
    return [...values]
      .sort((left, right) => left.localeCompare(right))
      .map((value) => ({ value, label: value }))
  }, [modelsQuery.data?.data?.items, normalizedGroup, props.initial])
  const endpointOptions = useMemo(
    () =>
      CHANNEL_TEST_ENDPOINT_TYPE_OPTIONS.map((option) => ({
        value: option.value,
        label: t(option.label),
      })),
    [t]
  )
  let modelField: ReactNode
  if (targetLocked) {
    modelField = <Input id='monitor-model' value={model} disabled />
  } else if (modelsQuery.isError) {
    modelField = (
      <Input
        id='monitor-model'
        value={model}
        placeholder={t('Failed to load')}
        disabled
      />
    )
  } else if (normalizedGroup === '' || modelsQuery.isLoading) {
    modelField = (
      <Input
        id='monitor-model'
        value={model}
        placeholder={
          normalizedGroup === '' ? t('Select a group') : t('Loading')
        }
        disabled
      />
    )
  } else {
    modelField = (
      <Combobox
        id='monitor-model'
        options={modelOptions}
        value={model}
        onValueChange={(value) => setModel(value ?? '')}
        placeholder={t('Select or enter model name')}
        emptyText={t('No model found.')}
        allowCustomValue
        openOnFocus={false}
      />
    )
  }

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!group.trim() || !model.trim()) {
      toast.error(t('Group and model are required'))
      return
    }
    const normalizedScheduleStart = scheduleStart.trim()
    const normalizedScheduleEnd = scheduleEnd.trim()
    if ((normalizedScheduleStart === '') !== (normalizedScheduleEnd === '')) {
      toast.error(t('Schedule start and end times must be set together'))
      return
    }
    props.onSubmit({
      group: group.trim(),
      model: model.trim(),
      endpoint_type: endpointType === 'auto' ? '' : endpointType,
      interval_seconds: Number(interval) || 1800,
      timeout_ms: Number(timeout) || 15000,
      schedule_start_time: normalizedScheduleStart,
      schedule_end_time: normalizedScheduleEnd,
      enabled,
    })
  }

  return (
    <form className='grid gap-4' onSubmit={submit}>
      <div className='grid gap-2'>
        <Label htmlFor='monitor-group'>{t('Group')}</Label>
        {targetLocked ? (
          <Input id='monitor-group' value={group} disabled />
        ) : (
          <Combobox
            id='monitor-group'
            options={groupOptions}
            value={group}
            onValueChange={(value) => {
              const nextGroup = value ?? ''
              if (nextGroup !== group) setModel('')
              setGroup(nextGroup)
            }}
            placeholder={groupsLoading ? t('Loading') : t('Select a group')}
            searchPlaceholder={t('Search...')}
            emptyText={t('No group found.')}
            openOnFocus={false}
          />
        )}
      </div>
      <div className='grid gap-2'>
        <Label htmlFor='monitor-model'>{t('Model to monitor')}</Label>
        {modelField}
      </div>
      <div className='grid gap-2'>
        <Label htmlFor='monitor-endpoint'>{t('Endpoint type')}</Label>
        <Select
          items={endpointOptions}
          value={endpointType}
          onValueChange={(value) => value !== null && setEndpointType(value)}
          disabled={targetLocked}
        >
          <SelectTrigger id='monitor-endpoint' className='w-full min-w-0'>
            <SelectValue className='min-w-0 truncate' />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {endpointOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>
      <FieldGroup className='gap-2'>
        <FieldGroup className='grid grid-cols-2 gap-3'>
          <Field>
            <FieldLabel htmlFor='monitor-interval'>
              {t('Active fallback interval (seconds)')}
            </FieldLabel>
            <Input
              id='monitor-interval'
              type='number'
              min={30}
              max={86400}
              value={interval}
              onChange={(event) => setInterval(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='monitor-timeout'>
              {t('Timeout (milliseconds)')}
            </FieldLabel>
            <Input
              id='monitor-timeout'
              type='number'
              min={1000}
              max={120000}
              value={timeout}
              onChange={(event) => setTimeout(event.target.value)}
            />
          </Field>
        </FieldGroup>
        <FieldDescription className='text-xs'>
          {t(
            'Real traffic updates status within one minute. Active checks run only after no valid traffic is seen for this interval.'
          )}
        </FieldDescription>
      </FieldGroup>
      <div className='grid gap-2'>
        <Label>{t('Daily check window (Beijing time)')}</Label>
        <p className='text-muted-foreground text-xs'>
          {t('Leave both blank for all-day monitoring')}
        </p>
        <div className='grid grid-cols-2 gap-3'>
          <div className='grid gap-2'>
            <Label htmlFor='monitor-schedule-start'>{t('Start Time')}</Label>
            <Input
              id='monitor-schedule-start'
              type='time'
              value={scheduleStart}
              onChange={(event) => setScheduleStart(event.target.value)}
            />
          </div>
          <div className='grid gap-2'>
            <Label htmlFor='monitor-schedule-end'>{t('End Time')}</Label>
            <Input
              id='monitor-schedule-end'
              type='time'
              value={scheduleEnd}
              onChange={(event) => setScheduleEnd(event.target.value)}
            />
          </div>
        </div>
      </div>
      <div className='flex items-center justify-between rounded-lg border px-3 py-2.5'>
        <Label htmlFor='monitor-enabled' className='cursor-pointer'>
          {t('Enable monitor')}
        </Label>
        <Switch
          id='monitor-enabled'
          checked={enabled}
          onCheckedChange={setEnabled}
        />
      </div>
      <DialogFooter>
        <DialogClose render={<Button type='button' variant='outline' />}>
          {t('Cancel')}
        </DialogClose>
        <Button type='submit' disabled={props.submitting}>
          {props.submitting ? t('Saving') : t('Save monitor')}
        </Button>
      </DialogFooter>
    </form>
  )
}

export function MonitorHistory(props: { monitor: ChannelMonitor }) {
  const { t } = useTranslation()
  const recent = (props.monitor.history ?? []).slice(
    -CHANNEL_MONITOR_HISTORY_LIMIT
  )
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
    <div
      className='min-w-0 flex-1'
      role='group'
      aria-label={t('Recent checks')}
      aria-orientation='horizontal'
      data-slot='channel-monitor-history-track'
    >
      <div className='flex h-5 w-full min-w-0 items-stretch gap-1 overflow-hidden'>
        {timeline.map(({ item, key }) => {
          const title = item
            ? `${formatTimestampToDate(item.started_at)} · ${getStatusLabel(item.status, t)} · ${formatLatency(item.response_time_ms)}`
            : t('No checks yet')
          return (
            <span
              key={key}
              title={title}
              aria-label={title}
              data-slot='channel-monitor-history-segment'
              data-state={item?.status ?? 'empty'}
              className={cn(
                'min-w-0 flex-1 rounded-full',
                item?.status === 'operational' && 'bg-emerald-500',
                item?.status === 'degraded' && 'bg-amber-500',
                item?.status === 'failed' && 'bg-red-500',
                item?.status === 'error' && 'bg-muted-foreground/40',
                !item && 'bg-muted-foreground/20'
              )}
            />
          )
        })}
      </div>
    </div>
  )
}

function RunDetails(props: { run: ChannelMonitorRun }) {
  const { t } = useTranslation()
  return (
    <div className='grid gap-3 rounded-lg border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-1.5'>
          <span
            className={cn(
              'rounded-full px-2 py-0.5 text-xs font-medium',
              STATUS_STYLES[props.run.status as ChannelMonitorStatus] ??
                'bg-muted text-muted-foreground'
            )}
          >
            {getStatusLabel(props.run.status, t)}
          </span>
          <span className='bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs font-medium'>
            {props.run.source === 'traffic'
              ? t('Real traffic')
              : t('Active check')}
          </span>
        </div>
        <span className='text-muted-foreground font-mono text-xs'>
          {formatLatency(props.run.response_time_ms)} ·{' '}
          {props.run.attempt_count} {t('attempts')}
        </span>
      </div>
      {props.run.error && (
        <p className='text-destructive text-xs'>{props.run.error}</p>
      )}
      <div className='grid gap-1.5'>
        {props.run.attempts.map((attempt) => (
          <div
            key={attempt.id}
            className='flex flex-wrap items-center justify-between gap-2 text-xs'
          >
            <span className='min-w-0 truncate'>
              <span className='text-muted-foreground mr-2 font-mono'>
                #{attempt.attempt_order}
              </span>
              {attempt.channel_name || `#${attempt.channel_id}`}
              <span className='text-muted-foreground ml-2'>
                {t('Priority')} {attempt.priority}
              </span>
            </span>
            <span
              className={cn(
                'font-mono',
                attempt.success ? 'text-emerald-600' : 'text-destructive'
              )}
            >
              {attempt.success ? t('Passed') : attempt.error || t('Failed')}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

export function ChannelMonitorAdmin() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((state) => state.auth.user)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<ChannelMonitor | undefined>()
  const [selected, setSelected] = useState<ChannelMonitor | undefined>()
  const [deleteTarget, setDeleteTarget] = useState<ChannelMonitor | undefined>()
  const [pendingRun, setPendingRun] = useState<
    { monitorId: number; taskId: string } | undefined
  >()
  const [formSession, setFormSession] = useState(0)
  const formSessionRef = useRef(0)
  const handledTaskIdRef = useRef<string | undefined>(undefined)
  const canOperate = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )
  const canWrite = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.WRITE
  )
  const canConfigure = canOperate && canWrite

  const monitorsQuery = useQuery({
    queryKey: monitorQueryKey,
    queryFn: getChannelMonitors,
    staleTime: 15_000,
  })
  const runsQuery = useQuery({
    queryKey: ['channel-monitor', 'runs', selected?.id],
    queryFn: () => getChannelMonitorRuns(selected?.id ?? 0),
    enabled: Boolean(selected?.id),
  })
  const monitorTaskQuery = useQuery({
    queryKey: [
      'channel-monitor',
      'task',
      pendingRun?.monitorId,
      pendingRun?.taskId,
    ],
    queryFn: async () => {
      if (!pendingRun) throw new Error('No pending monitor task')
      const result = await getChannelMonitorTask(
        pendingRun.monitorId,
        pendingRun.taskId
      )
      if (!result.data) {
        throw new Error(result.message || 'Monitor task missing')
      }
      return result.data
    },
    enabled: Boolean(pendingRun),
    retry: 2,
    refetchInterval: (query) => {
      if (query.state.status === 'error') return false
      const status = query.state.data?.status
      return !status || status === 'pending' || status === 'running'
        ? CHANNEL_MONITOR_TASK_POLL_INTERVAL_MS
        : false
    },
  })
  const refresh = () => void monitorsQuery.refetch()
  const taskStatus = monitorTaskQuery.data?.status
  const isMonitorTaskInProgress = Boolean(
    pendingRun &&
    !monitorTaskQuery.isError &&
    (!taskStatus || taskStatus === 'pending' || taskStatus === 'running')
  )

  useEffect(() => {
    if (!pendingRun) return
    if (monitorTaskQuery.isError) {
      const handledErrorId = `error:${pendingRun.taskId}`
      if (handledTaskIdRef.current === handledErrorId) return
      handledTaskIdRef.current = handledErrorId
      toast.error(t('Failed to run monitor'))
      return
    }
    const task = monitorTaskQuery.data
    if (!task || task.status === 'pending' || task.status === 'running') return
    if (handledTaskIdRef.current === task.task_id) return
    handledTaskIdRef.current = task.task_id

    void queryClient.invalidateQueries({ queryKey: monitorQueryKey })
    void queryClient.invalidateQueries({
      queryKey: ['channel-monitor', 'runs'],
    })
    void queryClient.invalidateQueries({
      queryKey: ['channel-monitor', 'status'],
    })
    if (task.status === 'failed') {
      toast.error(task.error || t('Failed to run monitor'))
    } else {
      toast.success(t('Channel test completed'))
    }
  }, [
    monitorTaskQuery.data,
    monitorTaskQuery.isError,
    pendingRun,
    queryClient,
    t,
  ])

  const saveMutation = useMutation({
    mutationFn: (request: {
      input: ChannelMonitorInput
      monitorId?: number
      session: number
    }) =>
      request.monitorId
        ? updateChannelMonitor(request.monitorId, request.input)
        : createChannelMonitor(request.input),
    onSuccess: (result, request) => {
      if (!result.success) {
        if (formSessionRef.current === request.session) {
          toast.error(result.message || t('Failed to save monitor'))
        }
        return
      }
      if (formSessionRef.current === request.session) {
        toast.success(t('Monitor saved'))
        formSessionRef.current += 1
        setFormSession(formSessionRef.current)
        setFormOpen(false)
        setEditing(undefined)
      }
      void queryClient.invalidateQueries({ queryKey: monitorQueryKey })
      void queryClient.invalidateQueries({
        queryKey: ['channel-monitor', 'status'],
      })
    },
    onError: (_error, request) => {
      if (formSessionRef.current === request.session) {
        toast.error(t('Failed to save monitor'))
      }
    },
  })
  const openMonitorForm = (monitor?: ChannelMonitor) => {
    saveMutation.reset()
    formSessionRef.current += 1
    setFormSession(formSessionRef.current)
    setEditing(monitor)
    setFormOpen(true)
  }

  const closeMonitorForm = () => {
    saveMutation.reset()
    formSessionRef.current += 1
    setFormSession(formSessionRef.current)
    setFormOpen(false)
    setEditing(undefined)
  }
  const runMutation = useMutation({
    mutationFn: runChannelMonitor,
    onSuccess: (result, monitorId) => {
      if (!result.success || !result.data?.task_id) {
        toast.error(result.message || t('Failed to run monitor'))
        return
      }
      handledTaskIdRef.current = undefined
      setPendingRun({ monitorId, taskId: result.data.task_id })
      toast.success(t('Monitor check queued'))
    },
    onError: () => toast.error(t('Failed to run monitor')),
  })
  const deleteMutation = useMutation({
    mutationFn: deleteChannelMonitor,
    onSuccess: (result, monitorId) => {
      if (result.success) {
        toast.success(t('Monitor deleted'))
        setDeleteTarget((current) =>
          current?.id === monitorId ? undefined : current
        )
        void queryClient.invalidateQueries({ queryKey: monitorQueryKey })
        void queryClient.invalidateQueries({
          queryKey: ['channel-monitor', 'status'],
        })
      } else {
        toast.error(result.message || t('Failed to delete monitor'))
      }
    },
    onError: () => toast.error(t('Failed to delete monitor')),
  })

  const monitors = monitorsQuery.data?.data?.items ?? []

  const monitorContent = () => {
    if (monitorsQuery.isLoading) {
      return (
        <div className='grid gap-3 sm:grid-cols-2'>
          <Skeleton className='h-52 rounded-xl' />
          <Skeleton className='h-52 rounded-xl' />
        </div>
      )
    }
    if (monitorsQuery.isError) {
      return (
        <div className='text-destructive flex min-h-56 items-center justify-center rounded-xl border border-dashed text-sm'>
          {t('Failed to load')}
        </div>
      )
    }
    if (monitors.length === 0) {
      return (
        <div className='text-muted-foreground flex min-h-56 items-center justify-center rounded-xl border border-dashed text-sm'>
          {t('No routing monitors configured')}
        </div>
      )
    }
    return (
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-3'>
        {monitors.map((monitor) => {
          const status = monitor.last_status as ChannelMonitorStatus
          return (
            <div key={monitor.id} className='bg-card rounded-xl border p-4'>
              <div className='flex items-start justify-between gap-3'>
                <div className='min-w-0'>
                  <p className='truncate text-sm font-semibold'>
                    {monitor.group}
                  </p>
                  <p className='text-muted-foreground mt-1 truncate text-xs'>
                    {monitor.model}
                  </p>
                  <p className='text-muted-foreground mt-1 truncate text-[11px]'>
                    {monitor.schedule_start_time && monitor.schedule_end_time
                      ? `${monitor.schedule_start_time} - ${monitor.schedule_end_time} · ${t('Beijing time')}`
                      : t('All day')}
                  </p>
                </div>
                <span
                  className={cn(
                    'shrink-0 rounded-full px-2 py-0.5 text-xs font-medium',
                    STATUS_STYLES[status] ?? 'bg-muted text-muted-foreground'
                  )}
                >
                  {getStatusLabel(status, t)}
                </span>
              </div>
              <div className='mt-3 grid grid-cols-3 gap-2 text-xs'>
                <div>
                  <p className='text-muted-foreground'>{t('7-day uptime')}</p>
                  <p className='mt-1 font-mono font-semibold'>
                    {formatAvailability(monitor.availability_7d)}
                  </p>
                </div>
                <div>
                  <p className='text-muted-foreground'>{t('30-day uptime')}</p>
                  <p className='mt-1 font-mono font-semibold'>
                    {formatAvailability(monitor.availability_30d)}
                  </p>
                </div>
                <div>
                  <p className='text-muted-foreground'>{t('Latency')}</p>
                  <p className='mt-1 font-mono font-semibold'>
                    {formatLatency(monitor.last_response_time_ms)}
                  </p>
                </div>
              </div>
              <div className='mt-3 flex min-h-5 min-w-0 items-center gap-2'>
                <MonitorHistory monitor={monitor} />
                <span className='text-muted-foreground shrink-0 text-[11px]'>
                  {monitor.enabled ? t('Enabled') : t('Paused')}
                </span>
              </div>
              <div className='mt-4 flex flex-wrap justify-end gap-1.5 border-t pt-3'>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => setSelected(monitor)}
                  aria-label={t('View monitor history')}
                  title={t('View monitor history')}
                >
                  <Eye />
                </Button>
                {canOperate && (
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => runMutation.mutate(monitor.id)}
                    disabled={runMutation.isPending || isMonitorTaskInProgress}
                    aria-label={t('Run monitor now')}
                    title={t('Run monitor now')}
                  >
                    <RotateCw
                      className={cn(
                        (runMutation.isPending ||
                          (isMonitorTaskInProgress &&
                            pendingRun?.monitorId === monitor.id)) &&
                          'animate-spin'
                      )}
                    />
                  </Button>
                )}
                {canConfigure && (
                  <Button
                    variant='ghost'
                    size='sm'
                    onClick={() => openMonitorForm(monitor)}
                    aria-label={t('Edit monitor')}
                    title={t('Edit monitor')}
                  >
                    {t('Edit')}
                  </Button>
                )}
                {canConfigure && (
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => setDeleteTarget(monitor)}
                    aria-label={t('Delete monitor')}
                    title={t('Delete monitor')}
                  >
                    <Trash2 className='text-destructive' />
                  </Button>
                )}
              </div>
            </div>
          )
        })}
      </div>
    )
  }

  const runsContent = () => {
    if (runsQuery.isLoading) return <Skeleton className='h-40 rounded-lg' />
    if (runsQuery.isError) {
      return <p className='text-destructive text-sm'>{t('Failed to load')}</p>
    }
    const runs = runsQuery.data?.data?.items ?? []
    if (runs.length === 0) {
      return (
        <p className='text-muted-foreground text-sm'>{t('No checks yet')}</p>
      )
    }
    return runs.map((run) => <RunDetails key={run.id} run={run} />)
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Monitor settings')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            onClick={refresh}
            disabled={monitorsQuery.isFetching}
            aria-label={t('Refresh')}
          >
            <RefreshCw
              className={cn(monitorsQuery.isFetching && 'animate-spin')}
            />
            <span className='hidden sm:inline'>{t('Refresh')}</span>
          </Button>
          {canConfigure && (
            <Button size='sm' onClick={() => openMonitorForm()}>
              <Plus />
              {t('Add monitor')}
            </Button>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          {monitorContent()}
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <Dialog
        open={formOpen}
        onOpenChange={(open) => {
          if (!open) closeMonitorForm()
        }}
      >
        <DialogContent className='max-w-lg'>
          <DialogHeader>
            <DialogTitle>
              {editing ? t('Edit monitor') : t('Add monitor')}
            </DialogTitle>
            <DialogDescription>
              {t(
                'Monitor the highest priority route and fall back when it fails.'
              )}
            </DialogDescription>
          </DialogHeader>
          <MonitorForm
            // Dialog keeps its children mounted while closed. Include the
            // open state so a fresh create form is mounted for every session.
            key={`${editing?.id ?? 'new'}-${formSession}`}
            initial={editing}
            submitting={saveMutation.isPending}
            onSubmit={(input) =>
              saveMutation.mutate({
                input,
                monitorId: editing?.id,
                session: formSession,
              })
            }
          />
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(selected)}
        onOpenChange={(open) => !open && setSelected(undefined)}
      >
        <DialogContent className='max-h-[min(80vh,720px)] max-w-2xl overflow-y-auto'>
          <DialogHeader>
            <DialogTitle>
              {selected?.group} · {selected?.model}
            </DialogTitle>
            <DialogDescription>
              {t('Priority attempts for recent checks')}
            </DialogDescription>
          </DialogHeader>
          <div className='grid gap-3'>{runsContent()}</div>
          <DialogFooter>
            <DialogClose render={<Button variant='outline' />}>
              {t('Close')}
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title={t('Delete monitor')}
        desc={t('Delete this monitor and its history?')}
        destructive
        confirmText={t('Delete')}
        isLoading={deleteMutation.isPending}
        handleConfirm={() => {
          if (deleteTarget) deleteMutation.mutate(deleteTarget.id)
        }}
      />
    </>
  )
}
