/** 构建响应中的内容身份按实际证据校验，缺失摘要不得靠推断补齐。 */
import type { BuildImage } from '../contract/generated'

/** @param mediaType 工具实际描述符类型 @returns 已登记的镜像或索引种类。 */
function descriptorKind(mediaType: BuildImage['manifestMediaType']): 'manifest' | 'index' | undefined {
  if (mediaType === 'application/vnd.oci.image.manifest.v1+json' || mediaType === 'application/vnd.docker.distribution.manifest.v2+json') return 'manifest'
  if (mediaType === 'application/vnd.oci.image.index.v1+json' || mediaType === 'application/vnd.docker.distribution.manifest.list.v2+json') return 'index'
  return undefined
}

/** @param value 已通过schema的镜像 @returns 本地ID、类型与工具实际导出证据一致，第三摘要仍拒绝。 */
export function boundImageID(value: BuildImage): boolean {
  if (value.manifestDigest === undefined) {
    return value.manifestMediaType === undefined && value.idKind === 'config' && value.configDigest === value.exportDigest && value.id === value.configDigest
  }
  const kind = descriptorKind(value.manifestMediaType)
  if (kind === undefined || value.exportDigest !== value.manifestDigest || value.configDigest === value.manifestDigest) return false
  return value.idKind === 'config' ? value.id === value.configDigest : value.idKind === kind && value.id === value.manifestDigest
}
