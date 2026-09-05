import { api } from '@/lib/api'

import type {
  ChannelMonitor,
  ChannelMonitorInput,
  ChannelMonitorRun,
  ChannelMonitorTask,
} from './types'

type MonitorListResponse = {
  success: boolean
  message?: string
  data?: { items: ChannelMonitor[] }
}

type MonitorModelListResponse = {
  success: boolean
  message?: string
  data?: { items: string[] }
}

type MonitorTaskResponse = {
  success: boolean
  message?: string
  data?: ChannelMonitorTask
}

function requireSuccessfulResponse<
  T extends { success: boolean; message?: string },
>(result: T): T {
  if (!result.success) {
    throw new Error(result.message || 'Request failed')
  }
  return result
}

export async function getChannelMonitors(): Promise<MonitorListResponse> {
  const response = await api.get<MonitorListResponse>('/api/channel-monitor/')
  return requireSuccessfulResponse(response.data)
}

export async function getChannelMonitorStatus(): Promise<MonitorListResponse> {
  const response = await api.get<MonitorListResponse>(
    '/api/channel-monitor/status'
  )
  return requireSuccessfulResponse(response.data)
}

export async function getChannelMonitorModels(
  group: string
): Promise<MonitorModelListResponse> {
  const response = await api.get<MonitorModelListResponse>(
    '/api/channel-monitor/models',
    { params: { group } }
  )
  return requireSuccessfulResponse(response.data)
}

export async function createChannelMonitor(
  input: ChannelMonitorInput
): Promise<{ success: boolean; message?: string; data?: ChannelMonitor }> {
  const response = await api.post('/api/channel-monitor/', input)
  return response.data
}

export async function updateChannelMonitor(
  id: number,
  input: Partial<ChannelMonitorInput>
): Promise<{ success: boolean; message?: string; data?: ChannelMonitor }> {
  const response = await api.put(`/api/channel-monitor/${id}`, input)
  return response.data
}

export async function deleteChannelMonitor(
  id: number
): Promise<{ success: boolean; message?: string }> {
  const response = await api.delete(`/api/channel-monitor/${id}`)
  return response.data
}

export async function runChannelMonitor(
  id: number
): Promise<{ success: boolean; message?: string; data?: { task_id: string } }> {
  const response = await api.post(`/api/channel-monitor/${id}/test`)
  return response.data
}

export async function getChannelMonitorTask(
  monitorId: number,
  taskId: string
): Promise<MonitorTaskResponse> {
  const response = await api.get<MonitorTaskResponse>(
    `/api/channel-monitor/${monitorId}/test/${encodeURIComponent(taskId)}`
  )
  return requireSuccessfulResponse(response.data)
}

export async function getChannelMonitorRuns(
  id: number,
  limit = 30
): Promise<{
  success: boolean
  message?: string
  data?: { items: ChannelMonitorRun[] }
}> {
  const response = await api.get(`/api/channel-monitor/${id}/runs`, {
    params: { limit },
  })
  return requireSuccessfulResponse(response.data)
}
