/** 在所有字节切分位置验证UTF-8、CRLF和半帧边界。 */
import { expect, it } from 'vitest'
import { SSEParser, type SSEFrame } from './sseParser'

it('UTF-8中文、BOM和CRLF在任意网络块切分后仍只交付完整帧', () => {
  const bytes = new TextEncoder().encode('\uFEFF: 心跳\r\nevent: state\r\nid: 9007199254740993\r\ndata: {"内容":\r\ndata: "完整"}\r\n\r\n')
  for (let index = 0; index <= bytes.length; index++) {
    const frames: SSEFrame[] = []; const parser = new SSEParser(4096, value => { frames.push(value) })
    parser.push(bytes.slice(0, index)); parser.push(bytes.slice(index)); parser.finish()
    expect(frames).toEqual([{ event: 'state', id: '9007199254740993', data: '{"内容":\n"完整"}' }])
  }
})

it('支持CR与LF，控制帧不继承业务id，EOF不提交没有空行的半帧', () => {
  const frames: SSEFrame[] = []; const parser = new SSEParser(4096, frame => { frames.push(frame) })
  parser.push(new TextEncoder().encode('id: 7\nevent: state\ndata: {}\n\nevent: caught_up\rdata: {}\r\revent: partial\ndata: 未结束'))
  parser.finish()
  expect(frames).toEqual([{ event: 'state', id: '7', data: '{}' }, { event: 'caught_up', id: null, data: '{}' }])
})

it('限制完整物理帧字节，拒绝非法UTF-8，忽略SSE注释与NUL id', () => {
  const frames: SSEFrame[] = []; const parser = new SSEParser(64, frame => { frames.push(frame) })
  parser.push(new TextEncoder().encode(': heartbeat\n\nretry: 2000\nunknown\nid: bad\0id\ndata: {}\n\n'))
  expect(frames).toEqual([{ event: 'message', id: null, data: '{}' }])
  expect(() => new SSEParser(4, () => { throw new Error('不得交付') }).push(new TextEncoder().encode('data:x\r\n\r\n'))).toThrow()
  const invalid = new SSEParser(4096, () => { throw new Error('不得交付') })
  expect(() => invalid.push(new Uint8Array([0xff, 10]))).toThrow('状态通知')
})
