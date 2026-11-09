/** 测试平台进程必须明确收回；发送信号不等于进程已经退出。 */

/** 未确认退出时上层保留目录和资源，不能丢弃唯一进程句柄。 */
class UnconfirmedExit extends Error {
  constructor() { super('测试平台强制终止后仍未确认退出，保留现场'); this.name = 'UnconfirmedExit' }
}
/** 判断聚合错误是否含未收回进程，供夹具决定是否允许清理目录。 */
export function isProcessExitUnconfirmed(error) {
  return error instanceof UnconfirmedExit || error instanceof AggregateError && error.errors.some(isProcessExitUnconfirmed)
}
/** 先安装退出监听器，再发送信号；任何结果都释放本次计时器及监听器。 */
async function terminate(child, signal, milliseconds) {
  if (child.exitCode !== null || child.signalCode !== null) return { code: child.exitCode, signal: child.signalCode, forced: false }
  return new Promise((resolve, reject) => {
    const finish = value => { clearTimeout(timer); child.removeListener('exit', exited); resolve(value) }
    const exited = (code, receivedSignal) => finish({ code, signal: receivedSignal, forced: signal === 'SIGKILL' })
    const timer = setTimeout(() => { child.removeListener('exit', exited); resolve(null) }, milliseconds)
    child.once('exit', exited)
    try { child.kill(signal) }
    catch (error) { clearTimeout(timer); child.removeListener('exit', exited); reject(error) }
  })
}
/** 优先正常退出；强杀后等待确认但仍明确报告非正常退出，不冒称正常完成。 */
export async function stopPlatformProcess(child, { timeoutMs = 15000, killTimeoutMs = 5000 } = {}) {
  if (child?.pid === undefined) return
  if (child.exitCode !== null || child.signalCode !== null) {
    if (child.exitCode !== 0) throw new Error('测试平台已经非正常退出')
    return
  }
  let result
  try { result = await terminate(child, 'SIGTERM', timeoutMs) }
  catch (error) { throw new AggregateError([error, new UnconfirmedExit()], '无法确认测试平台终止', { cause: error }) }
  if (result) {
    if (result.code !== 0) throw new Error('测试平台非正常退出')
    return
  }
  try { result = await terminate(child, 'SIGKILL', killTimeoutMs) }
  catch (error) { throw new AggregateError([error, new UnconfirmedExit()], '无法确认测试平台强制终止', { cause: error }) }
  if (!result) throw new UnconfirmedExit()
  throw new Error(result.forced ? '测试平台退出超时，已确认强制终止' : '测试平台退出超时，随后已确认退出')
}
/** 启动尚未返回时也拥有已spawn进程，失败必须先收回再向上报告。 */
export async function startOwnedPlatform(start, stop) {
  try { return await start() }
  catch (error) {
    try { await stop() }
    catch (cleanupError) { throw new AggregateError([error, cleanupError], '测试平台启动及进程收回均失败', { cause: cleanupError }) }
    throw error
  }
}
/** 先收集进程退出结果；已知异常退出仍可清理，未知退出必须保留现场。 */
export async function finishOwnedPlatform(failures, stop, cleanup) {
  const errors = [...failures]
  try { await stop() }
  catch (error) { errors.push(error) }
  if (!errors.some(isProcessExitUnconfirmed)) {
    try { await cleanup() }
    catch (error) { errors.push(error) }
  }
  if (errors.length === 1) throw errors[0]
  if (errors.length > 1) throw new AggregateError(errors, '浏览器验收及进程清理均失败')
}
