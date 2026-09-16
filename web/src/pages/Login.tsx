import { useEffect, useState } from 'react'
import { Button, Card, Divider, Form, Input, Space, Tabs } from 'antd'
import { GithubOutlined, LoginOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { get, post, setToken } from '../api'
import { t } from '../i18n'
import LangToggle from '../components/LangToggle'
import { PALETTE } from '../theme'
import { App as AntdApp } from 'antd'

interface Provider { id: string; name: string; type: string }

export default function Login() {
  const nav = useNavigate()
  const msg = AntdApp.useApp().message
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
      msg.error(e.message || t('Login failed'))
    }
  }

  const onRegister = async (v: {
    username: string; password: string; display_name?: string; tenant_name?: string
  }) => {
    try {
      const r = await post<{ token: string }>('/api/v1/auth/register', v)
      setToken(r.token)
      msg.success(t('Registered successfully; you are signed in'))
      nav('/apis', { replace: true })
    } catch (e: any) {
      msg.error(e.message || t('Registration failed')) // REGISTRATION_DISABLED 等错误原样展示
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
      background: PALETTE.bgLayout, position: 'relative',
    }}>
      {/* 登录页没有顶栏：语言切换固定在页面右上角 */}
      <div style={{ position: 'absolute', top: 16, right: 20 }}>
        <LangToggle />
      </div>
      <Card style={{ width: 400, boxShadow: '0 1px 4px rgba(0,0,0,.06)' }}>
        <div style={{ textAlign: 'center', marginBottom: 12 }}>
          <span style={{ fontSize: 20, fontWeight: 700, color: PALETTE.text }}>TestPilot</span>
          <div style={{ color: PALETTE.textSecondary, fontSize: 12 }}>{t('LLM-powered automated integration testing platform')}</div>
        </div>
        <Tabs
          centered
          items={[
            {
              key: 'login',
              label: t('Sign in'),
              children: (
                <Form layout="vertical" onFinish={onLogin}>
                  <Form.Item name="username" label={t('Username')} rules={[{ required: true }]}>
                    <Input autoFocus placeholder={t('Username')} />
                  </Form.Item>
                  <Form.Item name="password" label={t('Password')} rules={[{ required: true }]}>
                    <Input.Password placeholder={t('Password')} />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" block icon={<LoginOutlined />}>
                    {t('Sign in')}
                  </Button>
                </Form>
              ),
            },
            {
              key: 'register',
              label: t('Register'),
              children: (
                <Form layout="vertical" onFinish={onRegister}>
                  <Form.Item
                    name="username" label={t('Username')} rules={[{ required: true }, { min: 3, max: 64 }]}
                  >
                    <Input placeholder={t('3-64 characters')} />
                  </Form.Item>
                  <Form.Item
                    name="password" label={t('Password')} rules={[{ required: true }, { min: 8, max: 128 }]}
                  >
                    <Input.Password placeholder={t('At least 8 characters')} />
                  </Form.Item>
                  <Form.Item name="display_name" label={t('Display name (optional)')}>
                    <Input />
                  </Form.Item>
                  <Form.Item name="tenant_name" label={t('Tenant name (optional, defaults to username)')}>
                    <Input placeholder={t('A tenant is created for you on registration (you become its owner)')} />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" block>
                    {t('Register & sign in')}
                  </Button>
                </Form>
              ),
            },
          ]}
        />
        {providers.length > 0 && (
          <>
            <Divider style={{ margin: '12px 0' }}>
              <span style={{ color: PALETTE.textTertiary, fontSize: 12 }}>{t('Single sign-on')}</span>
            </Divider>
            <Space orientation="vertical" style={{ width: '100%' }}>
              {providers.map((p) => (
                <Button
                  key={p.id} block icon={<GithubOutlined />} onClick={() => sso(p)}
                >
                  {t('Sign in with {name}{oauth}', { name: p.name, oauth: p.type === 'oauth2' ? ' (OAuth2)' : '' })}
                </Button>
              ))}
            </Space>
          </>
        )}
      </Card>
    </div>
  )
}
