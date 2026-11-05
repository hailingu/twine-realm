/** 领域位置计算不依赖 React Flow 或浏览器缓存。 */
import { describe, expect, it } from 'vitest'
import { isDirty, movePosition, positionsOf, rebasePositions } from './model'
import { INITIAL_PROJECT } from '../testing/projectFixtures'

describe('布局草稿', () => {
  it('只改变稳定服务身份的位置，不改写确认快照', () => {
    const draft = positionsOf(INITIAL_PROJECT)
    expect(isDirty(INITIAL_PROJECT, draft)).toBe(false)
    const moved = movePosition(draft, INITIAL_PROJECT.nodes[0]?.id ?? '', { x: 250, y: 340 })
    expect(isDirty(INITIAL_PROJECT, moved)).toBe(true)
    expect(INITIAL_PROJECT.nodes[0]?.position).toEqual({ x: 100, y: 100 })
    expect(movePosition(moved, 'missing', { x: 0, y: 0 })).toEqual(moved)
    for (const x of [NaN, Infinity, 1000001]) expect(movePosition(moved, INITIAL_PROJECT.nodes[0]?.id ?? '', { x, y: 0 })).toBe(moved)
    expect(rebasePositions(INITIAL_PROJECT.nodes, moved)).toEqual(moved)
    expect(rebasePositions(INITIAL_PROJECT.nodes, [])).toEqual(draft)
  })
})
