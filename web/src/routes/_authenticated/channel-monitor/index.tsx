import { createFileRoute } from '@tanstack/react-router'

import { ChannelMonitorStatusPage } from '@/features/channel-monitor'

export const Route = createFileRoute('/_authenticated/channel-monitor/')({
  component: ChannelMonitorStatusPage,
})
