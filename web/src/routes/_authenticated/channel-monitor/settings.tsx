import { createFileRoute, redirect } from '@tanstack/react-router'

import { ChannelMonitorAdmin } from '@/features/channel-monitor/admin'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute(
  '/_authenticated/channel-monitor/settings'
)({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (
      !hasPermission(
        auth.user,
        ADMIN_PERMISSION_RESOURCES.CHANNEL,
        ADMIN_PERMISSION_ACTIONS.READ
      )
    ) {
      throw redirect({ to: '/403' })
    }
  },
  component: ChannelMonitorAdmin,
})
