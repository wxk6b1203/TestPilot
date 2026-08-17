import { Button, Checkbox, Input, Select, Tabs, Tag, Typography } from 'antd'
import { SaveOutlined, SendOutlined } from '@ant-design/icons'
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import useSaveShortcut from '../hooks/useSaveShortcut'
import { useShortcut } from '../hooks/useShortcut'
import { useLeaveGuard } from '../hooks/useLeaveGuard'
import { SHORTCUTS } from '../shortcuts'
import { get, post, put } from '../api'
import type { DebugResult, HttpApi } from '../api'
import BodyEditor from '../components/BodyEditor'
import type { BodyValue } from '../components/BodyEditor'
import KvEditor from '../components/KvEditor'
import type { Kv } from '../components/KvEditor'
import ResponsePane from '../components/ResponsePane'
import SplitPane from '../components/SplitPane'
import { METHOD_COLORS, PALETTE } from '../theme'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

// HttpApi 的脚本列（api.ts 类型未含，本地扩展：后端 JSON 列为 [{"lang","source"}]）
interface ScriptRow { lang: string; source: string }
type ApiFull = HttpApi & { pre_scripts?: ScriptRow[]; post_scripts?: ScriptRow[] }

const EMPTY_ROW: Kv = { key: '', value: '' }
const EMPTY_BODY: BodyValue = { contentType: 0, raw: '' }
const scriptOf = (rows?: ScriptRow[]) => rows?.find((s) => s.lang === 'python')?.source ?? ''

// 表单快照（用于 dirty 判定：与"已保存/已回填"时刻的快照对比）
const formOf = (name: string, method: number, uri: string, params: Kv[], headers: Kv[], cookies: Kv[], body: BodyValue, pre: string, post: string, settings: HttpApi['settings']) =>
  JSON.stringify({ name, method, uri, params, headers, cookies, body, preScript: pre, postScript: post, settings })

const methodOptions = Object.entries(METHOD_COLORS).map(([v, m]) => ({
  value: Number(v),
  label: <span style={{ color: m.color, fontWeight: 700 }}>{m.text}</span>,
}))

// API 调试工作区（旗舰页）：/apis/:id 加载已有接口；newMode（/apis 右侧）为空白新建形态。
// createParentId：右键目录「新建接口」进入时的目标目录树节点 id，保存时接口直接落到该目录。
export default function ApiDebug({ newMode, createParentId, onSaved }: { newMode?: boolean; createParentId?: string; onSaved?: () => void }) {
  const nav = useNavigate()
  const { id } = useParams()
  const { projectId, envId, envs } = useLayout()

  const [method, setMethod] = useState(1)
  const [name, setName] = useState('')
  const [uri, setUri] = useState('')
  const [params, setParams] = useState<Kv[]>([EMPTY_ROW])
  const [headers, setHeaders] = useState<Kv[]>([EMPTY_ROW])
  const [cookies, setCookies] = useState<Kv[]>([EMPTY_ROW])
  const [settings, setSettings] = useState<NonNullable<HttpApi['settings']>>({
    tls_verify: true, follow_redirects: true, comment_tolerant_json: false,
  })
  const [body, setBody] = useState<BodyValue>(EMPTY_BODY)
  const [preScript, setPreScript] = useState('')
  const [postScript, setPostScript] = useState('')
  const [savedId, setSavedId] = useState('')
  const [env, setEnv] = useState(envId)
  const [loading, setLoading] = useState(false)
  const [debugResult, setDebugResult] = useState<DebugResult>()
  const [savedSnapshot, setSavedSnapshot] = useState(() => formOf('', 1, '', [EMPTY_ROW], [EMPTY_ROW], [EMPTY_ROW], EMPTY_BODY, '', '', { tls_verify: true, follow_redirects: true, comment_tolerant_json: false }))
  const sendingRef = useRef(false)

  // 回填已有接口
  useEffect(() => {
    if (!id || newMode) return
    get<ApiFull>(`/api/v1/apis/${id}`)
      .then((a) => {
        const m = a.method || 1
        const u = a.uri || ''
        const p = a.params?.length ? a.params : [EMPTY_ROW]
        const h = a.headers?.length ? a.headers : [EMPTY_ROW]
        const ck = a.cookies?.length ? a.cookies.map((c) => ({ key: c.name, value: c.value })) : [EMPTY_ROW]
        const b = a.body ?? EMPTY_BODY
        const st = { tls_verify: a.settings?.tls_verify ?? true, follow_redirects: a.settings?.follow_redirects ?? true, comment_tolerant_json: a.settings?.comment_tolerant_json ?? false }
        const pre = scriptOf(a.pre_scripts)
        const post = scriptOf(a.post_scripts)
        setMethod(m)
        setName(a.name ?? '')
        setUri(u)
        setParams(p)
        setHeaders(h)
        setCookies(ck)
        setSettings(st)
        setBody(b)
        setPreScript(pre)
        setPostScript(post)
        setSavedId(String(a.id))
        setSavedSnapshot(formOf(a.name ?? '', m, u, p, h, ck, b, pre, post, st))
      })
      .catch((e) => message.error(e.message))
  }, [id, newMode])

  // 环境：默认跟随全局选择，工作区可临时改
  useEffect(() => setEnv(envId), [envId])

  const currentSnapshot = formOf(name, method, uri, params, headers, cookies, body, preScript, postScript, settings)
  const dirty = currentSnapshot !== savedSnapshot
  const { guard, allowOnce } = useLeaveGuard(dirty)

  const clean = (rows: Kv[]) => rows.filter((r) => r.key.trim() !== '')

  const send = async () => {
    if (!projectId || sendingRef.current) return
    if (!uri.trim()) {
      message.warning('请输入请求 URL')
      return
    }
    sendingRef.current = true
    setLoading(true)
    try {
      const r = await post<DebugResult>('/api/v1/apis/debug', {
        project_id: projectId,
        api_id: savedId || undefined,
        method,
        uri,
        params: clean(params),
        headers: clean(headers),
        cookies: clean(cookies).map((r) => ({ name: r.key, value: r.value })),
        settings,
        body: body.contentType === 0 ? undefined : body,
        env_id: env || undefined,
      })
      setDebugResult(r)
    } catch (e: any) {
      message.error(e.message)
    } finally {
      sendingRef.current = false
      setLoading(false)
    }
  }

  // Ctrl/Cmd + Enter 发送（按键定义集中在 src/shortcuts.ts）
  useShortcut(SHORTCUTS.send, send)

  const payload = () => ({
    project_id: projectId,
    name: name.trim(), // 始终发送：清空名称也要能保存（undefined 会被省略 → 后端保留旧值）
    method,
    uri,
    params: clean(params),
    headers: clean(headers),
    cookies: clean(cookies).map((r) => ({ name: r.key, value: r.value })),
    settings,
    body,
    pre_scripts: preScript.trim() ? [{ lang: 'python', source: preScript, enabled: true }] : undefined,
    post_scripts: postScript.trim() ? [{ lang: 'python', source: postScript, enabled: true }] : undefined,
  })

  const defaultName = () => name.trim() || `${METHOD_COLORS[method]?.text ?? 'GET'} ${uri}`.trim()

  const save = () => {
    if (savedId) void doUpdate()
    else void doCreate(defaultName()) // 新建时直接保存，不再弹确认 Modal
  }
  useSaveShortcut(save)

  const doUpdate = async () => {
    try {
      await put<HttpApi>(`/api/v1/apis/${savedId}`, payload())
      setSavedSnapshot(currentSnapshot)
      message.success('已保存')
      onSaved?.()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const doCreate = async (finalName?: string) => {
    const saveName = (finalName ?? defaultName()).trim()
    if (!saveName) {
      message.warning('请输入接口名称')
      return
    }
    try {
      const r = await post<HttpApi>('/api/v1/apis', {
        ...payload(),
        name: saveName, // 直接使用默认/当前名称
        parent_id: createParentId || undefined, // 右键目录新建：落到目标目录（0 省略 = 挂根）
      })
      message.success('已保存')
      setName(saveName)
      setSavedSnapshot(formOf(saveName, method, uri, params, headers, cookies, body, preScript, postScript, settings))
      allowOnce() // 新建成功后直接放行跳转，避免触发未保存确认
      onSaved?.()
      nav(`/apis/${r.id}`)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  if (!projectId)
    return (
      <div style={{
        height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: '#FFFFFF', color: PALETTE.textTertiary,
      }}>
        请先在顶部选择项目
      </div>
    )

  const scriptArea = (value: string, onChange: (v: string) => void, placeholder: string) => (
    <Input.TextArea
      rows={8}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
    />
  )

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: '#FFFFFF' }}>
      {/* 名称 / ID 独立一行（不挤在发送/保存栏） */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px',
        borderBottom: `1px solid ${PALETTE.border}`, flexShrink: 0,
      }}>
        <span style={{ fontSize: 12, color: PALETTE.textSecondary, flexShrink: 0 }}>名称</span>
        <Input
          size="small"
          style={{ width: 320 }}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="接口名称（可空，树/列表展示兜底 METHOD uri）"
        />
        <span style={{ flex: 1 }} />
        {savedId && (
          <Typography.Text
            copyable={{ text: savedId, tooltips: ['复制 ID', '已复制'] }}
            style={{ fontSize: 11, color: PALETTE.textTertiary, whiteSpace: 'nowrap' }}
          >
            ID {savedId}
          </Typography.Text>
        )}
      </div>

      {/* 工具栏：方法 + URL + 环境 + 发送/保存 */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px',
        borderBottom: `1px solid ${PALETTE.border}`, flexShrink: 0,
      }}>
        <Select style={{ width: 108 }} value={method} options={methodOptions} onChange={setMethod} />
        <Input
          style={{ flex: 1 }}
          value={uri}
          onChange={(e) => setUri(e.target.value)}
          onPressEnter={send}
          placeholder="输入请求 URL，如 /users/{id} 或 https://api.example.com/users"
        />
        <Select
          style={{ width: 150 }}
          value={env || undefined}
          placeholder="环境"
          allowClear
          options={envs.map((e) => ({ value: e.id, label: e.name }))}
          onChange={(v) => setEnv(v ?? '')}
        />
        <Button type="primary" icon={<SendOutlined />} loading={loading} onClick={send}>发送</Button>
        <Button icon={<SaveOutlined />} onClick={save}>保存</Button>
        {dirty && <Tag color="warning" style={{ marginInlineEnd: 0 }}>未保存</Tag>}
      </div>

      {/* 请求编排 tabs（上部）与响应面板（下部）——可拖拽分栏 */}
      <div style={{ flex: 1, minHeight: 0 }}>
        <SplitPane direction="vertical" initial="45%" min="20%" max="85%">
          <div style={{ height: '100%', overflow: 'auto', padding: '8px 16px' }}>
            <Tabs
          size="small"
          items={[
            {
              key: 'params',
              label: '参数',
              children: <KvEditor value={params} onChange={setParams} />,
            },
            {
              key: 'headers',
              label: '请求头',
              children: (
                <KvEditor
                  value={headers}
                  onChange={setHeaders}
                  keyPlaceholder="Header 名"
                  valuePlaceholder="Header 值（支持 {{var}}）"
                />
              ),
            },
            {
              key: 'cookies',
              label: 'Cookies',
              children: (
                <KvEditor
                  value={cookies}
                  onChange={setCookies}
                  keyPlaceholder="Cookie 名"
                  valuePlaceholder="Cookie 值（支持 {{var}}）"
                />
              ),
            },
            {
              key: 'body',
              label: '请求体',
              children: <BodyEditor value={body} onChange={setBody} />,
            },
            {
              key: 'settings',
              label: '设置',
              children: (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, maxWidth: 420 }}>
                  <Checkbox
                    checked={settings.tls_verify}
                    onChange={(e) => setSettings({ ...settings, tls_verify: e.target.checked })}
                  >
                    校验 TLS 证书（关闭仅用于自签名测试环境）
                  </Checkbox>
                  <Checkbox
                    checked={settings.follow_redirects}
                    onChange={(e) => setSettings({ ...settings, follow_redirects: e.target.checked })}
                  >
                    跟随重定向
                  </Checkbox>
                  <Checkbox
                    checked={settings.comment_tolerant_json}
                    onChange={(e) => setSettings({ ...settings, comment_tolerant_json: e.target.checked })}
                  >
                    兼容带注释 JSON（请求体）
                  </Checkbox>
                </div>
              ),
            },
            {
              key: 'pre',
              label: '前置脚本',
              children: scriptArea(preScript, setPreScript, '# 请求发送前执行（Python）\n# 示例：ctx.set_var("now", ...)'),
            },
            {
              key: 'post',
              label: '后置脚本',
              children: scriptArea(postScript, setPostScript, '# 响应返回后执行（Python）\n# 示例：resp = ctx.response'),
            },
          ]}
        />
          </div>
          <div style={{
            height: '100%', overflow: 'hidden', padding: '8px 16px',
            borderTop: `1px solid ${PALETTE.border}`,
          }}>
            <ResponsePane result={debugResult} loading={loading} />
          </div>
        </SplitPane>
      </div>

      {guard}
    </div>
  )
}
