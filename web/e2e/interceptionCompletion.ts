/** 拦截回调以成功结果承载异常，避免等待者尚未挂接时发生未处理拒绝。 */
export function interceptionCompletion() {
  let complete!: (result: { ok: true } | { ok: false; error: unknown }) => void
  const outcome = new Promise<{ ok: true } | { ok: false; error: unknown }>(resolve => { complete = resolve })
  return {
    run: async (action: () => Promise<void>) => {
      try { await action(); complete({ ok: true }) }
      catch (error) { complete({ ok: false, error }) }
    },
    wait: async () => { const result = await outcome; if (!result.ok) throw result.error }
  }
}
