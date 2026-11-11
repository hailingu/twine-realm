/** 拦截受理与模拟断流必须都完成，才允许断言客户端恢复入口。 */
import { expect, test } from 'vitest'
import { interceptionCompletion } from '../../e2e/interceptionCompletion'

/** 手工释放异步边界，不依赖真实延迟。 */
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(done => { resolve = done })
  return { promise, resolve }
}

test('真实响应和断流均完成后才放行，不以点击或响应开始代替完成', async () => {
  const response = deferred(); const aborted = deferred()
  const completion = interceptionCompletion(); let inspected = false; let finished = false
  const waiting = completion.wait().then(() => { finished = true })
  const running = completion.run(async () => { await response.promise; inspected = true; await aborted.promise })
  await Promise.resolve(); expect(inspected).toBe(false); expect(finished).toBe(false)
  response.resolve(); await Promise.resolve(); expect(inspected).toBe(true); expect(finished).toBe(false)
  aborted.resolve(); await running; await waiting; expect(finished).toBe(true)
})
test.each(['非202', '回执解析失败', '断流失败'])('%s传播至等待者，回调自身无未处理拒绝', async message => {
  const completion = interceptionCompletion(); const error = new Error(message)
  await expect(completion.run(() => Promise.reject(error))).resolves.toBeUndefined()
  await expect(completion.wait()).rejects.toBe(error)
})
