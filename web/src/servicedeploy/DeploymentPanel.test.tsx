/** 部署审阅与受理分开，未知提交保持原键，草稿、禁用和旧事实均不能产生假就绪。 */
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'
import { DEPLOY_COMMAND, DEPLOY_PLAN, DEPLOY_RECEIPT, EMPTY_DEPLOYMENT, READY_DEPLOYMENT, fakeDeploymentAPI } from '../testing/deploymentFixtures'
import { READY_BUILD, fakeBuildAPI } from '../testing/buildFixtures'
import { RequestError } from '../project/api'
import { DeploymentPanel } from './DeploymentPanel'

afterEach(cleanup)

/** @returns 每个场景独立的界面端口与相同原服务身份。 */
function setup() {
  const api = fakeDeploymentAPI(); const buildApi = fakeBuildAPI(); buildApi.snapshot.mockResolvedValue(READY_BUILD)
  const props = { api, buildApi, projectId: DEPLOY_PLAN.projectId, serviceId: DEPLOY_PLAN.serviceId, revision: '1', bindings: [DEPLOY_PLAN.binding], blocked: false }
  return { api, buildApi, props }
}

it('先审阅精确计划，明确确认才接受部署，202不显示业务就绪', async () => {
  const { api, props } = setup(); const user = userEvent.setup()
  render(<DeploymentPanel {...props} />)
  await waitFor(() => { expect(screen.getByRole('button', { name: '审阅部署' })).toBeEnabled() })
  await user.click(screen.getByRole('button', { name: '审阅部署' }))
  expect(await screen.findByLabelText('部署预览')).toBeVisible()
  expect(api.accept).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: '确认部署到本机' }))
  await waitFor(() => { expect(api.accept).toHaveBeenCalledTimes(1) })
  expect(api.accept.mock.calls[0]?.[2]).toMatchObject({ preview: DEPLOY_PLAN.request, planDigest: DEPLOY_PLAN.digest })
  expect(screen.queryByRole('link', { name: '打开业务入口 ↗' })).toBeNull()
  expect(screen.getByRole('button', { name: '审阅部署' })).toBeDisabled()
})

it('未知提交重试沿用原键和原计划，核实原回执期间禁止新部署', async () => {
  const { api, props } = setup(); const user = userEvent.setup()
  api.accept.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '受理响应丢失' }, 0))
  render(<DeploymentPanel {...props} />)
  await waitFor(() => { expect(screen.getByRole('button', { name: '审阅部署' })).toBeEnabled() })
  await user.click(screen.getByRole('button', { name: '审阅部署' }))
  await user.click(await screen.findByRole('button', { name: '确认部署到本机' }))
  await screen.findByRole('button', { name: '核实原部署请求' })
  const original = api.accept.mock.calls[0]?.[2]
  expect(original).toBeDefined()
  expect(screen.getByRole('spinbutton', { name: '业务入口端口' })).toBeDisabled()
  api.receipt.mockRejectedValueOnce(new RequestError({ code: 'MUTATION_NOT_FOUND', message: '尚未找到原键' }, 404))
  await user.click(screen.getByRole('button', { name: '核实原部署请求' }))
  expect(await screen.findByText('尚未找到原键')).toBeVisible()
  await user.click(screen.getByRole('button', { name: '重试原部署请求' }))
  await waitFor(() => { expect(api.accept).toHaveBeenCalledTimes(2) })
  expect(api.accept.mock.calls[1]?.[2]).toEqual(original)
})

it('真实拒绝保留安全诊断并要求重审，旧画布预览不可确认', async () => {
  const { api, props } = setup(); const user = userEvent.setup()
  api.accept.mockRejectedValueOnce(new RequestError({ code: 'DEPLOY_PLAN_CHANGED', message: '预览已过期' }, 409))
  const view = render(<DeploymentPanel {...props} />)
  await waitFor(() => { expect(screen.getByRole('button', { name: '审阅部署' })).toBeEnabled() })
  await user.click(screen.getByRole('button', { name: '审阅部署' }))
  view.rerender(<DeploymentPanel {...props} revision="2" />)
  expect(screen.getByRole('button', { name: '确认部署到本机' })).toBeDisabled()
  view.rerender(<DeploymentPanel {...props} />)
  await user.click(screen.getByRole('button', { name: '确认部署到本机' }))
  expect(await screen.findByText('本次请求未受理：预览已过期')).toBeVisible()
  expect(screen.queryByLabelText('部署预览')).toBeNull()
})

it('只有新鲜且交叉验证的真实就绪显示独立入口，草稿和禁用不接受新目标', async () => {
  const { api, props } = setup()
  api.snapshot.mockResolvedValue(READY_DEPLOYMENT)
  const view = render(<DeploymentPanel {...props} blocked />)
  expect(await screen.findByRole('link', { name: '打开业务入口 ↗' })).toHaveAttribute('href', 'http://127.0.0.1:8081')
  expect(screen.getByRole('button', { name: '审阅部署' })).toBeDisabled()
  view.unmount()
  api.snapshot.mockResolvedValue({ ...READY_DEPLOYMENT, freshness: 'stale' })
  render(<DeploymentPanel {...props} />)
  expect(await screen.findByText(/状态需核实/)).toBeVisible()
  expect(screen.queryByRole('link', { name: '打开业务入口 ↗' })).toBeNull()
})

it('未装配或候选读取失败明确展示，副作用不因轮询失败自动重试', async () => {
  const { api, props } = setup()
  const view = render(<DeploymentPanel {...props} api={undefined} />)
  expect(screen.getByText('部署入口未配置。')).toBeVisible()
  view.unmount()
  api.snapshot.mockRejectedValue(new Error('状态暂不可读'))
  render(<DeploymentPanel {...props} />)
  expect(await screen.findByRole('alert')).toHaveTextContent('状态暂不可读')
  expect(api.accept).not.toHaveBeenCalled()
  expect(api.preview).not.toHaveBeenCalled()
})

it('原键核实不能接受另一计划的回执或用历史事实伪造当前就绪', async () => {
  const { api, props } = setup(); const user = userEvent.setup()
  api.accept.mockRejectedValueOnce(new Error('提交结果未确认'))
  api.receipt.mockResolvedValue({ ...DEPLOY_RECEIPT, mutationId: DEPLOY_COMMAND.mutationId })
  api.snapshot.mockResolvedValue(EMPTY_DEPLOYMENT)
  render(<DeploymentPanel {...props} />)
  await waitFor(() => { expect(screen.getByRole('button', { name: '审阅部署' })).toBeEnabled() })
  await user.click(screen.getByRole('button', { name: '审阅部署' }))
  await user.click(await screen.findByRole('button', { name: '确认部署到本机' }))
  await user.click(await screen.findByRole('button', { name: '核实原部署请求' }))
  expect(await screen.findByText(/部署响应与当前项目、服务或原请求不一致/)).toBeVisible()
  expect(screen.getByRole('button', { name: '审阅部署' })).toBeDisabled()
})
