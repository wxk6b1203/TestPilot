import { useEffect, useState } from 'react'
import { Button, Card, Divider, Form, Input, Space, Tabs, message } from 'antd'
import { GithubOutlined, LoginOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { get, post, setToken } from '../api'
import { PALETTE } from '../theme'

interface Provider { id: string; name: string; type: string }

export default function Login() {
  const nav = useNavigate()
  const [msg, ctx] = message.useMessage()
  const [providers, setProviders] = useState<Provider[]>([])

  useEffect(() => {
    get<{ items: Provider[] }>('/api/v1/auth/oidc/providers')
      .then((r) => setProviders(r.items))
      .catch(() => {})
  }, [])

  const onLogin = async (v: { username: string; password: string }) => {
    try {
      const r = await post<{ token: string }>('/api/v1/auth/login', v)
      setToken(r.token)
      nav('/apis', { replace: true })
    } catch (e: any) {
      msg.error(e.message || '登录失败')
    }
  }

  const onRegister = async (v: {
    username: string; password: string; display_name?: string; tenant_name?: string
  }) => {
    try {
      const r = await post<{ token: string }>('/api/v1/auth/register', v)
      setToken(r.token)
      msg.success('注册成功，已登录')
      nav('/apis', { replace: true })
    } catch (e: any) {
      msg.error(e.message || '注册失败') // REGISTRATION_DISABLED 等错误原样展示
    }
  }

  const sso = (p: Provider) => {
    // 浏览器 SSO 流：后端 302 回跳 #/auth/callback?token=…
    const origin = window.location.origin
    window.location.href =
      `/api/v1/auth/oidc/${p.id}/login?redirect=${encodeURIComponent(origin)}`
  }

  return (
    <div style={{
      minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: PALETTE.bgLayout,
    }}>
      {ctx}
      <Card style={{ width: 400, boxShadow: '0 1px 4px rgba(0,0,0,.06)' }}>
        <div style={{ textAlign: 'center', marginBottom: 12 }}>
          <span style={{ fontSize: 20, fontWeight: 700, color: PALETTE.text }}>TestPilot</span>
          <div style={{ color: PALETTE.textSecondary, fontSize: 12 }}>LLM 驱动的自动化集成测试平台</div>
        </div>
        <Tabs
          centered
          items={[
            {
              key: 'login',
              label: '登录',
              children: (
                <Form layout="vertical" onFinish={onLogin} initialValues={{ username: 'admin' }}>
                  <Form.Item name="username" label="用户名" rules={[{ required: true }]}>
                    <Input autoFocus placeholder="admin" />
                  </Form.Item>
                  <Form.Item name="password" label="密码" rules={[{ required: true }]}>
                    <Input.Password placeholder="admin123（初始种子账号）" />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" block icon={<LoginOutlined />}>
                    登录
                  </Button>
                </Form>
              ),
            },
            {
              key: 'register',
              label: '注册',
              children: (
                <Form layout="vertical" onFinish={onRegister}>
                  <Form.Item
                    name="username" label="用户名" rules={[{ required: true }, { min: 3, max: 64 }]}
                  >
                    <Input placeholder="3-64 字符" />
                  </Form.Item>
                  <Form.Item
                    name="password" label="密码" rules={[{ required: true }, { min: 8, max: 128 }]}
                  >
                    <Input.Password placeholder="至少 8 位" />
                  </Form.Item>
                  <Form.Item name="display_name" label="昵称（可选）">
                    <Input />
                  </Form.Item>
                  <Form.Item name="tenant_name" label="租户名（可选，默认=用户名）">
                    <Input placeholder="注册即自建租户并成为 owner" />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" block>
                    注册并登录
                  </Button>
                </Form>
              ),
            },
          ]}
        />
        {providers.length > 0 && (
          <>
            <Divider style={{ margin: '12px 0' }}>
              <span style={{ color: PALETTE.textTertiary, fontSize: 12 }}>第三方登录</span>
            </Divider>
            <Space direction="vertical" style={{ width: '100%' }}>
              {providers.map((p) => (
                <Button
                  key={p.id} block icon={<GithubOutlined />} onClick={() => sso(p)}
                >
                  使用 {p.name} 登录{p.type === 'oauth2' ? '（OAuth2）' : ''}
                </Button>
              ))}
            </Space>
          </>
        )}
      </Card>
    </div>
  )
}
