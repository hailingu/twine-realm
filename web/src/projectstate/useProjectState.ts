/** React会话拥有读取生命周期和本地新鲜度时钟，不拥有编辑或部署命令。 */
import { useEffect, useRef, useState } from 'react'
import type { StateAPI } from './api'
import { ProjectFeed, type FeedValue } from './feed'

/** @param api 可选读取边界 @param id 项目会话 @returns 统一状态投影、时钟及显式重连。 */
export function useProjectState(api: StateAPI | null | undefined, id: string) {
  const [value, setValue] = useState<FeedValue>({ snapshot: null, phase: 'connecting', message: '' })
  const [now, setNow] = useState(Date.now)
  const [attempt, setAttempt] = useState(0)
  const epoch = useRef(0)
  useEffect(() => {
    if (!api) return
    const session = ++epoch.current; const controller = new AbortController()
    const feed = new ProjectFeed(api, id, controller.signal, incoming => {
      if (!controller.signal.aborted && epoch.current === session) setValue(previous => ({ ...incoming, snapshot: incoming.snapshot ?? previous.snapshot }))
    })
    void feed.run().catch(() => { if (!controller.signal.aborted && epoch.current === session) setValue(previous => ({ ...previous, phase: 'failed', message: '状态读取内部异常，请保留草稿并重新连接' })) })
    return () => { controller.abort() }
  }, [api, id, attempt])
  useEffect(() => { const timer = window.setInterval(() => { setNow(Date.now()) }, 1000); return () => { window.clearInterval(timer) } }, [])
  const sameProject = !value.snapshot || value.snapshot.project.id === id
  return { ...value, snapshot: sameProject ? value.snapshot : null, phase: sameProject ? value.phase : 'connecting' as const, message: sameProject ? value.message : '', now, managed: Boolean(api), reconnect: () => { setAttempt(previous => previous + 1) } }
}
