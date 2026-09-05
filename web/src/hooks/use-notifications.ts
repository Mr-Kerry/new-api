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
import { useQuery } from '@tanstack/react-query'
import { useState, useMemo } from 'react'

import { useStatus } from '@/hooks/use-status'
import { getNotice } from '@/lib/api'
import { useNotificationStore } from '@/stores/notification-store'

function hashString(input: string): string {
  let hash = 0
  if (!input) return '0'

  for (let i = 0; i < input.length; i += 1) {
    const chr = input.charCodeAt(i)
    hash = (hash << 5) - hash + chr
    hash |= 0
  }

  return hash.toString(36)
}

/**
 * Generate a unique key for an announcement
 * Prefer backend id, fall back to a content hash so edits register
 */
function getAnnouncementKey(item: Record<string, unknown>): string {
  if (!item) return ''

  if (item.id !== undefined && item.id !== null) {
    return `id:${item.id}`
  }

  const fingerprint = JSON.stringify({
    publishDate: (item?.publishDate as string) || '',
    content: ((item?.content as string) || '').trim(),
    extra: ((item?.extra as string) || '').trim(),
    type: (item?.type as string) || '',
    title: ((item?.title as string) || '').trim(),
    link: ((item?.link as string) || '').trim(),
  })
  return `hash:${hashString(fingerprint)}`
}

function getLatestAnnouncement(
  announcements: Record<string, unknown>[]
): Record<string, unknown> | null {
  return announcements.reduce<Record<string, unknown> | null>((latest, item) => {
    if (!latest) return item

    const latestTime = new Date(String(latest.publishDate || '')).getTime()
    const itemTime = new Date(String(item.publishDate || '')).getTime()
    if (Number.isNaN(latestTime)) return item
    if (Number.isNaN(itemTime)) return latest
    return itemTime > latestTime ? item : latest
  }, null)
}

/**
 * Hook to manage notifications (Notice + Announcements)
 * Provides unread counts and read status management
 */
export function useNotifications() {
  const [popoverOpen, setPopoverOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<'notice' | 'announcements'>(
    'notice'
  )

  // Fetch Notice from API
  const {
    data: noticeResponse,
    isLoading: noticeLoading,
    refetch: refetchNotice,
  } = useQuery({
    queryKey: ['notice'],
    queryFn: getNotice,
    staleTime: 1000 * 60 * 5, // 5 minutes
  })

  // Fetch Announcements from status
  const { status, loading: statusLoading } = useStatus()
  const announcementsEnabled = status?.announcements_enabled ?? false
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const announcements: Record<string, unknown>[] = useMemo(() => {
    if (!announcementsEnabled) return []

    return [...((status?.announcements || []) as Record<string, unknown>[])]
      .sort((a, b) => {
        const aTime = new Date(String(a.publishDate || '')).getTime()
        const bTime = new Date(String(b.publishDate || '')).getTime()
        if (Number.isNaN(aTime)) return 1
        if (Number.isNaN(bTime)) return -1
        return bTime - aTime
      })
      .slice(0, 20)
  }, [announcementsEnabled, status?.announcements])

  // Notification store
  const {
    lastReadNotice,
    markNoticeRead,
    markAnnouncementsRead,
    isAnnouncementRead,
  } = useNotificationStore()

  // Extract notice content
  const noticeContent = noticeResponse?.success
    ? (noticeResponse.data || '').trim()
    : ''
  const latestAnnouncement = getLatestAnnouncement(announcements)
  const latestAnnouncementContent = String(latestAnnouncement?.content || '').trim()
  const displayedNotice = latestAnnouncementContent || noticeContent
  const displayedNoticeAnnouncement = latestAnnouncementContent
    ? latestAnnouncement
    : null

  // Calculate unread counts
  const unreadCounts = useMemo(() => {
    const noticeUnread = latestAnnouncementContent
      ? 0
      : noticeContent && noticeContent !== lastReadNotice
        ? 1
        : 0

    const announcementsUnread = announcements.filter(
      (item: Record<string, unknown>) => {
        const key = getAnnouncementKey(item)
        return !isAnnouncementRead(key)
      }
    ).length

    return {
      notice: noticeUnread,
      announcements: announcementsUnread,
      total: noticeUnread + announcementsUnread,
    }
  }, [noticeContent, latestAnnouncementContent, lastReadNotice, announcements, isAnnouncementRead])

  const markAnnouncementsAsRead = () => {
    if (announcements.length > 0) {
      const allKeys = announcements.map((item: Record<string, unknown>) =>
        getAnnouncementKey(item)
      )
      markAnnouncementsRead(allKeys)
    }
  }

  // Handle popover open
  const handleOpenPopover = (tab?: 'notice' | 'announcements') => {
    const nextTab = tab || activeTab

    // Mark currently visible content as read when opening the notification center
    if (displayedNoticeAnnouncement) {
      markAnnouncementsRead([getAnnouncementKey(displayedNoticeAnnouncement)])
    } else if (noticeContent) {
      markNoticeRead(noticeContent)
    }
    if (nextTab === 'announcements') {
      markAnnouncementsAsRead()
    }

    setActiveTab(nextTab)
    setPopoverOpen(true)
  }

  const handlePopoverOpenChange = (open: boolean) => {
    if (open) {
      handleOpenPopover(activeTab)
      return
    }

    setPopoverOpen(false)
  }

  // Handle tab change - mark announcements as read when switching to that tab
  const handleTabChange = (tab: 'notice' | 'announcements') => {
    setActiveTab(tab)

    if (tab === 'announcements') {
      markAnnouncementsAsRead()
    }
  }

  return {
    // Data
    notice: displayedNotice,
    noticeAnnouncement: displayedNoticeAnnouncement,
    announcements,
    loading: noticeLoading || statusLoading,

    // Unread counts
    unreadCount: unreadCounts.total,
    unreadNoticeCount: unreadCounts.notice,
    unreadAnnouncementsCount: unreadCounts.announcements,

    // Popover state
    popoverOpen,
    setPopoverOpen: handlePopoverOpenChange,
    activeTab,
    setActiveTab: handleTabChange,

    // Actions
    openPopover: handleOpenPopover,
    closePopover: () => setPopoverOpen(false),
    refetchNotice,
  }
}
