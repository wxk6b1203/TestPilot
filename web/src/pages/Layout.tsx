import { Layout as ALayout, Select, Space, Dropdown, Tag, Avatar, Typography } from 'antd'
import {
  ApiOutlined, ExperimentOutlined, ThunderboltOutlined, FileTextOutlined,
  ClusterOutlined, PlayCircleOutlined, EnvironmentOutlined, ProjectOutlined,
  SettingOutlined, DesktopOutlined, RobotOutlined, LogoutOutlined, DownOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import {
  get, getEnvId, getProjectId, post, setEnvId, setProjectId, setToken,
} from '../api'
import type { Environment, ListResp, Me, Project, TenantView } from '../api'
import { PALETTE, SPACING } from '../theme'
import type { LayoutCtx } from '../hooks/useLayout'
import { message } from '../messageBridge'

// 图标栏导航（IDE 式一级功能栏：图标在上、文字在下）
const NAV = [
  { path: '/apis', label: '接口', icon: <ApiOutlined /> },
  { path: '/cases', label: '用例', icon: <ExperimentOutlined /> },
  { path: '/suites', label: '套件', icon: <ClusterOutlined /> },
  { path: '/scripts', label: '脚本', icon: <FileTextOutlined /> },
  { path: '/plans', label: '计划', icon: <PlayCircleOutlined /> },
  { path: '/runs', label: '运行', icon: <ThunderboltOutlined /> },
  { path: '/stress', label: '压测', icon: <ThunderboltOutlined /> },
  { path: '/grpc', label: 'gRPC', icon: <ApiOutlined /> },
  { path: '/envs', label: '环境', icon: <EnvironmentOutlined /> },
  { path: '/certs', label: '证书', icon: <SafetyCertificateOutlined /> },
  { path: '/projects', label: '项目', icon: <ProjectOutlined /> },
  { path: '/admin', label: '管理', icon: <SettingOutlined />, admin: true },
  { path: '/workers', label: 'Worker', icon: <DesktopOutlined /> },
  { path: '/copilot', label: 'Copilot', icon: <RobotOutlined /> },
]

const ROLE_NAMES: Record<number, string> = { 1: 'owner', 2: 'admin', 3: 'member', 4: 'viewer' }

export default function Layout() {
  const nav = useNavigate()
  const loc = useLocation()
  const [projects, setProjects] = useState<Project[]>([])
  const [projectId, setPid] = useState(getProjectId())
  const [envs, setEnvs] = useState<Environment[]>([])
  const [envId, setEid] = useState(getEnvId())
  const [me, setMe] = useState<Me | null>(null)
  const [tenants, setTenants] = useState<TenantView[]>([])

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
    get<Me>('/api/v1/me').then(setMe).catch(() => {})
    get<{ items: TenantView[] }>('/api/v1/tenants').then((r) => setTenants(r.items)).catch(() => {})
  }, [])

  // 项目变化 → 环境列表重载
  useEffect(() => {
    if (!projectId) return
    get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}&page_size=100`)
      .then((r) => {
        setEnvs(r.items)
        // 环境已不存在时连同 localStorage 一起清，避免刷新后复活
        if (envId && !r.items.find((e) => e.id === envId)) {
          setEid('')
          setEnvId('')
        }
      })
      .catch(() => {})
  }, [projectId])

  const visibleNav = useMemo(
    () => NAV.filter((n) => !n.admin || (me?.role ?? 9) <= 2),
    [me],
  )

  const ctx: LayoutCtx = {
    projectId,
    projects,
    refreshProjects: () =>
      get<ListResp<Project>>('/api/v1/projects?page_size=100').then((r) => setProjects(r.items)),
    envId,
    setEnvId: (id) => {
      setEid(id)
      setEnvId(id)
    },
    envs,
    refreshEnvs: () =>
      get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}&page_size=100`)
        .then((r) => setEnvs(r.items)),
    me,
    tenants,
    switchTenant: async (tid) => {
      const r = await post<{ token: string }>('/api/v1/auth/switch-tenant', { tenant_id: tid })
      setToken(r.token)
      location.reload()
    },
    refreshMe: () => get<Me>('/api/v1/me').then(setMe),
  }

  return (
    <ALayout style={{ height: '100vh' }}>
      {/* 顶栏：品牌 + 项目/环境 + 租户切换 + 用户 */}
      <ALayout.Header
        style={{
          height: 48, lineHeight: '48px', background: PALETTE.topbar,
          padding: `0 ${SPACING[4]}px`, display: 'flex', alignItems: 'center', gap: SPACING[3],
        }}
      >
        <span style={{ fontWeight: 700, fontSize: 15, color: PALETTE.text }}>TestPilot</span>
        <Select
          size="small" style={{ width: 180 }} value={projectId || undefined}
          placeholder="选择项目"
          options={projects.map((p) => ({ value: p.id, label: p.name }))}
          onChange={(v) => {
            setPid(v)
            setProjectId(v)
          }}
        />
        <Select
          size="small" style={{ width: 150 }} value={envId || undefined}
          placeholder="环境"
          allowClear
          options={envs.map((e) => ({ value: e.id, label: e.name }))}
          onChange={(v) => {
            setEid(v ?? '')
            setEnvId(v ?? '')
          }}
        />
        <span style={{ flex: 1 }} />
        {tenants.length > 0 && (
          <Dropdown
            menu={{
              items: tenants.map((t) => ({
                key: t.tenant_id,
                label: `${t.name}（${ROLE_NAMES[t.role] ?? t.role}）${t.is_current ? ' ✓' : ''}`,
                disabled: t.is_current,
              })),
              onClick: ({ key }) => ctx.switchTenant(key),
            }}
          >
            <Typography.Link style={{ color: PALETTE.textSecondary, fontSize: 13 }}>
              {tenants.find((t) => t.is_current)?.name ?? '租户'} <DownOutlined style={{ fontSize: 10 }} />
            </Typography.Link>
          </Dropdown>
        )}
        <Dropdown
          menu={{
            items: [{ key: 'logout', icon: <LogoutOutlined />, label: '退出登录' }],
            onClick: () => {
              setToken(null)
              nav('/login', { replace: true })
            },
          }}
        >
          <Space size={6} style={{ cursor: 'pointer' }}>
            <Avatar size={26} style={{ background: '#4AC778', fontSize: 12 }}>
              {(me?.user?.username ?? '?').slice(0, 1).toUpperCase()}
            </Avatar>
            <span style={{ fontSize: 13, color: PALETTE.text }}>
              {me?.user?.display_name || me?.user?.username || '用户'}
            </span>
            {me && <Tag style={{ margin: 0 }} color={me.role <= 2 ? 'blue' : 'default'}>{ROLE_NAMES[me.role]}</Tag>}
          </Space>
        </Dropdown>
      </ALayout.Header>
      <ALayout style={{ height: 'calc(100vh - 48px)', flexDirection: 'row' }}>
        {/* 一级图标栏：导航项超出视口高度时纵向滚动 */}
        <div className="tp-nav-rail" style={{
          width: 72, background: PALETTE.bgLayout, borderRight: `1px solid ${PALETTE.border}`,
          display: 'flex', flexDirection: 'column', alignItems: 'center', paddingTop: SPACING[2], gap: 2,
          overflowY: 'auto', overflowX: 'hidden', minHeight: 0,
        }}>
          {visibleNav.map((n) => {
            const active = loc.pathname.startsWith(n.path)
            return (
              <div
                key={n.path}
                onClick={() => nav(n.path)}
                style={{
                  width: 56, padding: '7px 0', textAlign: 'center', cursor: 'pointer',
                  borderRadius: 8, marginBottom: 2,
                  color: active ? PALETTE.primary : PALETTE.textSecondary,
                  background: active ? '#E4EBFF' : 'transparent',
                }}
              >
                <div style={{ fontSize: 18 }}>{n.icon}</div>
                <div style={{ fontSize: 11 }}>{n.label}</div>
              </div>
            )
          })}
        </div>
        {/* 内容区 */}
        <div style={{ flex: 1, minWidth: 0, height: '100%', overflow: 'auto' }}>
          <Outlet context={ctx} />
        </div>
      </ALayout>
    </ALayout>
  )
}

