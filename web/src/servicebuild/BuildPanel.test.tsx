/** 面板只展示当前确认输入，历史候选、未保存草稿和未知提交各自有明确保护。 */
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'
import { BUILD_CANDIDATE, EMPTY_BUILD, READY_BUILD, RUNNING_BUILD, fakeBuildAPI } from '../testing/buildFixtures'
import { RequestError } from '../project/api'
import { BuildPanel } from './BuildPanel'

afterEach(cleanup)

it('配置单证据只显示真实本地内容，不冒称缺失的manifest', async () => {
  const image = BUILD_CANDIDATE.image
  const candidate = { ...BUILD_CANDIDATE, image: { id: image.id, idKind: 'config' as const, exportDigest: image.id, configDigest: image.id, tag: image.tag, binaryDigest: image.binaryDigest } }
  const api = fakeBuildAPI(); api.snapshot.mockResolvedValue({ ...READY_BUILD, candidate })
  render(<BuildPanel api={api} projectId={EMPTY_BUILD.projectId} serviceId={EMPTY_BUILD.serviceId} revision="1" blocked={false} />)
  expect(await screen.findByText('本地配置内容证明；构建工具未提供独立 manifest 描述符。')).toBeVisible()
  expect(screen.getByText('本地镜像内容 · config')).toBeVisible()
  expect(screen.queryByText('镜像 manifest / index')).toBeNull()
})

it('构建成功显示镜像及输入追溯，未保存草稿阻止新的副作用', async () => {
  const api = fakeBuildAPI(); api.snapshot.mockResolvedValue(READY_BUILD)
  const view = render(<BuildPanel api={api} projectId={EMPTY_BUILD.projectId} serviceId={EMPTY_BUILD.serviceId} revision="1" blocked />)
  expect(await screen.findByText('当前固定输入已通过构建')).toBeVisible()
  expect(screen.getByText('镜像 manifest / index')).toBeVisible()
  expect(screen.getByRole('button', { name: '检查并构建固定 commit' })).toBeDisabled()
  view.rerender(<BuildPanel api={api} projectId={EMPTY_BUILD.projectId} serviceId={EMPTY_BUILD.serviceId} revision="1" blocked={false} />)
  expect(screen.getByRole('button', { name: '检查并构建固定 commit' })).toBeEnabled()
})

it('在途操作只能明确取消，未知请求提供原键核实入口', async () => {
  const api = fakeBuildAPI(); api.snapshot.mockResolvedValue(RUNNING_BUILD)
  api.cancel.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
  const user = userEvent.setup()
  render(<BuildPanel api={api} projectId={EMPTY_BUILD.projectId} serviceId={EMPTY_BUILD.serviceId} revision="1" blocked={false} />)
  const cancel = await screen.findByRole('button', { name: '取消并核实原构建' })
  expect(screen.getByRole('button', { name: '检查并构建固定 commit' })).toBeDisabled()
  await user.click(cancel)
  expect(await screen.findByRole('button', { name: '核实原构建请求' })).toBeVisible()
  expect(screen.getByRole('button', { name: '重试原构建请求' })).toBeVisible()
  expect(screen.getByRole('button', { name: '取消并核实原构建' })).toBeDisabled()
})

it('没有适配器、未固定源码或历史候选时不会显示当前就绪', async () => {
  const view = render(<BuildPanel api={undefined} projectId={EMPTY_BUILD.projectId} serviceId={EMPTY_BUILD.serviceId} revision="1" blocked={false} />)
  expect(screen.getByText('构建入口未配置。')).toBeVisible()
  const api = fakeBuildAPI(); api.snapshot.mockResolvedValue({ ...READY_BUILD, candidateCurrent: false, candidateCode: 'INPUT_CHANGED' })
  view.rerender(<BuildPanel api={api} projectId={EMPTY_BUILD.projectId} serviceId={EMPTY_BUILD.serviceId} revision="1" blocked={false} />)
  expect(await screen.findByText('历史镜像候选 · 当前尚未确认可用')).toBeVisible()
  expect(screen.queryByText('当前固定输入已通过构建')).toBeNull()
})
