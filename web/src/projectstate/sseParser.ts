/** 有界UTF-8 SSE分帧，不把EOF半帧提升成已收到的业务事件。 */
import { invalidState } from './model'

/** 单帧只携带实际出现的id；协议控制帧不得继承上一个业务id。 */
export interface SSEFrame { event: string; id: string | null; data: string }

/** 每个读取会话独占解析器，CR/LF/CRLF和UTF-8均可跨任意网络块。 */
export class SSEParser {
  private line: number[] = []
  private bytes = 0
  private pendingCR = false
  private firstLine = true
  private event = ''
  private id: string | null = null
  private data: string[] = []
  private readonly decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })
  /** @param maximum 完整传输帧上限 @param receive 当前会话同步接收者。 */
  constructor(private readonly maximum: number, private readonly receive: (frame: SSEFrame) => void) {}
  /** @param chunk 任意网络字节 @returns 只在明确空行后交付完整帧。 */
  push(chunk: Uint8Array): void {
    for (const byte of chunk) {
      if (this.pendingCR) { this.pendingCR = false; this.newline(byte === 10 ? 2 : 1); if (byte === 10) continue }
      if (byte === 13) { this.pendingCR = true; continue }
      if (byte === 10) { this.newline(1); continue }
      this.bytes++; if (this.bytes > this.maximum) throw invalidState()
      this.line.push(byte)
    }
  }
  /** @returns 结清最后CR行结束符，丢弃没有空行结束的半帧。 */
  finish(): void { if (this.pendingCR) { this.pendingCR = false; this.newline(1) } }
  /** @param size 实际换行字节数 @returns 受限的一行或完整帧。 */
  private newline(size: number): void {
    this.bytes += size; if (this.bytes > this.maximum) throw invalidState()
    let line: string
    try { line = this.decoder.decode(new Uint8Array(this.line)) } catch { throw invalidState() }
    this.line = []
    if (this.firstLine) { this.firstLine = false; if (line.startsWith('\uFEFF')) line = line.slice(1) }
    if (!line) {
      const frame = { event: this.event || 'message', id: this.id, data: this.data.join('\n') }
      this.event = ''; this.id = null; this.data = []; this.bytes = 0
      if (frame.data) this.receive(frame)
      return
    }
    this.field(line)
  }
  /** @param line 已解码且有界的非空行 @returns 只接受SSE定义的数据字段。 */
  private field(line: string): void {
    if (line.startsWith(':')) return
    const colon = line.indexOf(':'); const field = colon < 0 ? line : line.slice(0, colon)
    let value = colon < 0 ? '' : line.slice(colon + 1); if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') this.event = value
    if (field === 'data') this.data.push(value)
    if (field === 'id' && !value.includes('\0')) this.id = value
  }
}
