import { createFileRoute, Navigate } from '@tanstack/react-router'
import { TopupStore } from '@/features/top-store'
import { useIsSidebarModuleVisible } from '@/hooks/use-sidebar-config'

function TopStoreRoute() {
  const visible = useIsSidebarModuleVisible('/top-store')

  if (!visible) {
    return <Navigate to='/wallet' replace />
  }

  return <TopupStore />
}

export const Route = createFileRoute('/_authenticated/top-store')({
  component: TopStoreRoute,
})