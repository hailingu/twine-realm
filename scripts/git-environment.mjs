/** 为子工具和临时仓库隔离外层 Git hook 的仓库、索引及配置覆盖。 */
export function isolatedGitEnvironment(env = process.env) {
  return Object.fromEntries(Object.entries(env).filter(([key]) => !key.startsWith('GIT_')))
}
