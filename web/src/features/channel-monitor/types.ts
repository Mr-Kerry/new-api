export type ChannelMonitorStatus =
  | 'operational'
  | 'degraded'
  | 'failed'
  | 'error'

export type ChannelMonitorRunSource = 'active' | 'traffic'

export type ChannelMonitorTaskStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'failed'

export interface ChannelMonitorTask {
  task_id: string
  status: ChannelMonitorTaskStatus
  error?: string
}

export interface ChannelMonitorHistoryItem {
  status: ChannelMonitorStatus
  started_at: number
  response_time_ms: number
}

export interface ChannelMonitor {
  id: number
  group: string
  model: string
  endpoint_type?: string
  enabled: boolean
  interval_seconds: number
  timeout_ms: number
  schedule_start_time?: string
  schedule_end_time?: string
  last_run_at: number
  last_status?: ChannelMonitorStatus | string
  last_response_time_ms: number
  last_channel_id?: number
  last_channel_name?: string
  last_priority?: number
  last_error?: string
  last_degraded: boolean
  availability_7d: number
  availability_30d: number
  user_ratio?: number
  history: ChannelMonitorHistoryItem[]
  updated_at: number
}

export interface ChannelMonitorAttempt {
  id: number
  run_id: number
  attempt_order: number
  channel_id: number
  channel_name: string
  priority: number
  success: boolean
  response_time_ms: number
  error?: string
}

export interface ChannelMonitorRun {
  id: number
  monitor_id: number
  source: ChannelMonitorRunSource
  status: ChannelMonitorStatus | string
  started_at: number
  finished_at: number
  response_time_ms: number
  final_channel_id: number
  final_channel_name: string
  final_priority: number
  degraded: boolean
  attempt_count: number
  error?: string
  attempts: ChannelMonitorAttempt[]
}

export interface ChannelMonitorInput {
  group: string
  model: string
  endpoint_type?: string
  enabled?: boolean
  interval_seconds?: number
  timeout_ms?: number
  schedule_start_time?: string
  schedule_end_time?: string
}
