import { Button, Card, Form, Input, message } from 'antd'
import { useNavigate } from 'react-router-dom'
import { post, setToken } from '../api'

export default function Login() {
  const nav = useNavigate()
  const [msg, ctx] = message.useMessage()

  const onFinish = async (v: { username: string; password: string }) => {
    try {
      const r = await post<{ token: string }>('/api/v1/auth/login', v)
      setToken(r.token)
      nav('/runs', { replace: true })
    } catch (e: any) {
      msg.error(e.message || '登录失败')
    }
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#f0f2f5' }}>
      {ctx}
      <Card title="TestPilot 控制台" style={{ width: 380 }}>
        <Form layout="vertical" onFinish={onFinish} initialValues={{ username: 'admin' }}>
          <Form.Item name="username" label="用户名" rules={[{ required: true }]}>
            <Input autoFocus />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true }]}>
            <Input.Password onPressEnter={() => undefined} />
          </Form.Item>
          <Button type="primary" htmlType="submit" block>登录</Button>
        </Form>
      </Card>
    </div>
  )
}
