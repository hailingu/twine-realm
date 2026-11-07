/** 单一读取拥有者分别维护完整信封游标与实际重放游标，不确认任何编辑。 */
import type { StateSnapshot } from '../contract/generated'
import { RequestError } from '../project/api'
import { StateReadError, type StateAPI } from './api'
import { invalidState, ownedState, stateNumber, type StateMessage } from './model'

/** 状态运输阶段不代表业务操作成功或资源已经就绪。 */
export type FeedPhase = 'connecting' | 'catchingUp' | 'live' | 'disconnected' | 'failed'
/** 所有者发送不可变投影；不包含命令、原请求键或本地编辑草稿。 */
export interface FeedValue { snapshot: StateSnapshot | null; phase: FeedPhase; message: string }

/** @param milliseconds 有界等待 @param signal 读取寿命 @returns 可取消且同步释放监听器的等待。 */
function pause(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise(resolve => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve() }
    const timer = setTimeout(finish, milliseconds)
    signal.addEventListener('abort', finish, { once: true }); if (signal.aborted) finish()
  })
}

/** 一个项目会话拥有一个流和最多一个完整读取；断开只回收这些读取。 */
export class ProjectFeed {
  private current: StateSnapshot | null = null
  private cursor: string | null = null
  private caughtUp: string | null = null
  private connected = false
  private restore = true
  private delaySeconds = 2
  private stream: AbortController | null = null
  private refreshTask: Promise<void> | null = null
  private refreshRequested = false
  private streamFailure: unknown = null
  /** @param api 唯一读取端口 @param id 项目 @param signal 读取会话 @param receive 当前投影拥有者。 */
  constructor(private readonly api: StateAPI, private readonly id: string, private readonly signal: AbortSignal, private readonly receive: (value: FeedValue) => void) {}

  /** @returns 拥有退避、窗口恢复及所有在途读取的完整寿命。 */
  async run(): Promise<void> {
    while (!this.signal.aborted) {
      try {
        await this.connect()
      } catch (error) {
        if (!await this.recover(error)) return
      } finally {
        await this.closeReading()
      }
    }
  }

  /** @returns 本轮先读取一致信封，再从实际重放游标建立唯一流。 */
  private async connect(): Promise<void> {
    this.streamFailure = null
    await this.readFull(this.signal)
    if (this.signal.aborted) return
    if (this.restore && this.current) { this.cursor = this.current.cursor; this.restore = false }
    this.connected = true; this.caughtUp = null
    this.publish()
    const stream = new AbortController(); this.stream = stream
    await this.api.events(this.id, this.cursor ?? '0', AbortSignal.any([this.signal, stream.signal]), message => { if (this.stream === stream && !stream.signal.aborted) this.accept(message) })
    throw new StateReadError('状态连接已结束，正在按原游标恢复')
  }

  /** @param error 本轮失败 @returns 仅按本轮错误决定恢复、退避或结束。 */
  private async recover(error: unknown): Promise<boolean> {
    const failure = this.streamFailure ?? error
    await this.closeReading()
    if (this.signal.aborted) return false
    const reason = this.failure(failure)
    this.connected = false; this.caughtUp = null; this.restore = reason.resync
    this.receive({ snapshot: this.current, phase: reason.terminal ? 'failed' : 'disconnected', message: reason.message })
    if (reason.terminal) return false
    await pause(Math.max(this.delaySeconds * 1000, reason.retryMs ?? 0), this.signal)
    this.delaySeconds = Math.min(30, this.delaySeconds * 2)
    return !this.signal.aborted
  }

  /** @returns 先围栏旧流，再收回它拥有的在途快照读取。 */
  private async closeReading(): Promise<void> {
    this.stream?.abort(); this.stream = null
    if (this.refreshTask) await this.refreshTask
  }

  /** @param error 未信任边界失败 @returns 安全文案与明确恢复策略。 */
  private failure(error: unknown): StateReadError {
    if (error instanceof StateReadError) return error
    if (error instanceof RequestError) return new StateReadError(error.message, true)
    return new StateReadError('状态读取暂不可用，保留最后事实并退避重连')
  }

  /** @param signal 当前读取寿命 @returns 不回退已应用版本的一致信封。 */
  private async readFull(signal: AbortSignal): Promise<void> {
    const value = ownedState(await this.api.snapshot(this.id, signal), this.id)
    if (signal.aborted || this.signal.aborted) return
    if (this.current && (stateNumber(value.cursor) < stateNumber(this.current.cursor) || stateNumber(value.project.revision) < stateNumber(this.current.project.revision))) throw invalidState()
    if (this.current?.cursor === value.cursor && Date.parse(value.capturedAt) < Date.parse(this.current.capturedAt)) return
    this.current = value; this.publish()
  }

  /** @param message 已验证且来自本流的消息 @returns 只推进实际收到的连续游标。 */
  private accept(message: StateMessage): void {
    if (this.signal.aborted || this.stream?.signal.aborted) return
    if (message.value.projectId !== this.id) throw invalidState()
    if (message.kind === 'resync') throw new StateReadError('事件窗口已改变，正在读取完整状态并保留草稿', true)
    if (message.kind === 'caughtUp') {
      if (message.value.cursor !== this.cursor) throw invalidState()
      this.caughtUp = message.value.cursor; this.delaySeconds = 2
      this.refresh(); this.publish(); return
    }
    const incoming = stateNumber(message.value.sequence); const previous = stateNumber(this.cursor ?? '0')
    if (incoming <= previous) return
    if (incoming !== previous + 1n) throw invalidState()
    this.cursor = message.value.sequence
    this.refresh(); this.publish()
  }

  /** @returns 合并高频通知为最多一个读取；HTTP结果领先时不跳过重放游标。 */
  private refresh(): void {
    const stream = this.stream
    if (!stream || stream.signal.aborted || !this.cursor || this.current && stateNumber(this.current.cursor) >= stateNumber(this.cursor)) return
    this.refreshRequested = true
    if (this.refreshTask) return
    const signal = AbortSignal.any([this.signal, stream.signal])
    this.refreshTask = this.refreshLoop(signal).catch((error: unknown) => { if (!signal.aborted) { this.streamFailure = error; stream.abort() } }).finally(() => { this.refreshTask = null; if (this.refreshRequested && !signal.aborted) this.refresh() })
  }

  /** @param signal 当前流读取寿命 @returns 收回所有合并读取，不遗留Promise或计时器。 */
  private async refreshLoop(signal: AbortSignal): Promise<void> {
    do {
      this.refreshRequested = false; await this.readFull(signal)
    } while (!signal.aborted && this.refreshRequested && this.current && this.cursor && stateNumber(this.current.cursor) < stateNumber(this.cursor))
  }

  /** @returns 只有连续重放且最新视图已经追平，才显示运输已恢复。 */
  private publish(): void {
    if (this.signal.aborted) return
    const live = this.connected && this.caughtUp === this.cursor && this.current && this.cursor && stateNumber(this.current.cursor) >= stateNumber(this.cursor)
    let phase: FeedPhase = 'connecting'
    if (this.connected) phase = 'catchingUp'
    if (live) phase = 'live'
    this.receive({ snapshot: this.current, phase, message: '' })
  }
}
