/** 业务服务观测随当前时钟失效，不把持久操作成功当作实时就绪。 */
import type { DeploySnapshot } from '../contract/generated'

/** @param value 同库服务投影 @param error 当前读取是否未知 @param now 展示时刻 @returns 分开的运行、健康、就绪与过期结果。 */
export function deploymentLabel(value: DeploySnapshot | undefined, error: string, now: number): { text: string; tone: string } {
  if (!value?.target) return { text: '尚未部署', tone: 'unknown' }
  const fact = value.observation
  if (error || !fact || value.freshness === 'unknown') return { text: '业务运行事实未知', tone: 'unknown' }
  if (value.freshness === 'stale' || now - Date.parse(fact.observedAt) > 20000) return { text: '业务观测已过期', tone: 'unknown' }
  if (value.observationCode || fact.state === 'drifted') return { text: '业务资源漂移 · 需核实', tone: 'error' }
  if (fact.ready && fact.running && fact.healthy) return { text: '业务运行 · 健康且就绪', tone: 'ready' }
  if (fact.running) return { text: fact.healthy ? '业务存活 · 尚未就绪' : '业务运行 · 健康未确认', tone: 'pending' }
  return { text: fact.state === 'not_created' ? '未发现业务容器' : '业务已停止', tone: 'stopped' }
}
