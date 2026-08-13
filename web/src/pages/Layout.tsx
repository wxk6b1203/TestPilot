import { Layout as ALayout, Menu, Select, Space, Typography, Dropdown, message } from 'antd'
import { LogoutOutlined, RobotOutlined } from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { Outlet, useLocation, useNavigate, useOutletContext } from 'react-router-dom'
import { get, getProjectId, setProjectId, setToken } from '../api'
import type { ListResp, Project } from '../api'

const menuItems = [
  { key: '/copilot', label: 'Copilot' },
  { key: '/runs', label: '运行' },
  { key: '/plans', label: '测试计划' },
  { key: '/cases', label: '测试用例' },
  { key: '/apis', label: '接口' },
  { key: '/stress', label: '压测' },
  { key: '/envs', label: '环境与变量' },
  { key: '/projects', label: '项目' },
  { key: '/workers', label: 'Worker' },
]

export default function Layout() {
  const nav = useNavigate()
  const loc = useLocation()
  const [projects, setProjects] = useState<Project[]>([])
  const [projectId, setPid] = useState(getProjectId())

  useEffect(() => {
    get<ListResp<Project>>('/api/v1/projects?page_size=100')
      .then((r) => {
        setProjects(r.items)
        if (!r.items.find((p) => p.id === projectId)) {
          const first = r.items[0]?.id || ''
          setPid(first)
          setProjectId(first)
        }
      })
      .catch((e) => message.error(e.message))
  }, [])

  const ctx: LayoutCtx = {
    projectId,
    projects,
    refreshProjects: () =>
      get<ListResp<Project>>('/api/v1/projects?page_size=100').then((r) => setProjects(r.items)),
  }

  return (
    <ALayout style={{ minHeight: '100vh' }}>
      <ALayout.Sider theme="dark" width={200}>
        <div style={{ color: '#fff', padding: 16, fontSize: 16, fontWeight: 600 }}>
          <RobotOutlined /> TestPilot
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[loc.pathname]}
          items={menuItems}
          onClick={(e) => nav(e.key)}
        />
      </ALayout.Sider>
      <ALayout>
        <ALayout.Header style={{ background: '#fff', display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0 24px' }}>
          <Space>
            <Typography.Text type="secondary">项目</Typography.Text>
            <Select
              style={{ width: 240 }}
              value={projectId || undefined}
              placeholder="选择项目"
              options={projects.map((p) => ({ value: p.id, label: p.name }))}
              onChange={(v) => {
                setPid(v)
                setProjectId(v) // 子页通过 useLayout().projectId 依赖自动重载
              }}
            />
          </Space>
          <Dropdown
            menu={{
              items: [{ key: 'logout', icon: <LogoutOutlined />, label: '退出登录' }],
              onClick: () => {
                setToken(null)
                nav('/login', { replace: true })
              },
            }}
          >
            <Typography.Link>admin</Typography.Link>
          </Dropdown>
        </ALayout.Header>
        <ALayout.Content style={{ margin: 16 }}>
          <Outlet context={ctx} />
        </ALayout.Content>
      </ALayout>
    </ALayout>
  )
}

export interface LayoutCtx {
  projectId: string
  projects: Project[]
  refreshProjects: () => Promise<void>
}

export function useLayout() {
  return useOutletContext<LayoutCtx>()
}
