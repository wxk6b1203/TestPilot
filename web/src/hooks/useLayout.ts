import { useOutletContext } from 'react-router-dom'
import type { Environment, Me, Project, TenantView } from '../api'

// Layout 顶栏下发的全局上下文（项目/环境/身份）。
// 单独成文件：Layout.tsx 只导出组件（fast refresh），各页面从这里取 hook。
export interface LayoutCtx {
  projectId: string
  projects: Project[]
  refreshProjects: () => Promise<void>
  envId: string
  setEnvId: (id: string) => void
  envs: Environment[]
  refreshEnvs: () => Promise<void>
  me: Me | null
  tenants: TenantView[]
  switchTenant: (tenantId: string) => Promise<void>
  refreshMe: () => Promise<void>
}

export function useLayout() {
  return useOutletContext<LayoutCtx>()
}
