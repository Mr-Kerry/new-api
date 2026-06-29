import { createFileRoute, Navigate } from '@tanstack/react-router'
import { CodexRadar } from '@/features/radar'
import { useIsSidebarModuleVisible } from '@/hooks/use-sidebar-config'

function RadarRoute() {
  const visible = useIsSidebarModuleVisible('/radar')

  if (!visible) {
    return <Navigate to='/dashboard/overview' replace />
  }

  return <CodexRadar />
}

export const Route = createFileRoute('/_authenticated/radar')({
  component: RadarRoute,
})
// import { createFileRoute } from '@tanstack/react-router'
// import { CodexRadar } from '@/features/radar'

// export const Route = createFileRoute('/_authenticated/radar')({
//   component: CodexRadar,
// })