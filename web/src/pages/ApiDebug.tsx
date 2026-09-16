import { Button, Checkbox, Input, Select, Tabs, Tag, Tooltip, Typography } from 'antd'
import { CodeOutlined, SaveOutlined, SendOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import useSaveShortcut from '../hooks/useSaveShortcut'
import { useShortcut } from '../hooks/useShortcut'
import { useLeaveGuard } from '../hooks/useLeaveGuard'
import { SHORTCUTS } from '../shortcuts'
import { get, post, put } from '../api'
import type { DebugResult, FieldDesign, HttpApi } from '../api'
import ApiDesignPanel, { designMeta, designOf } from '../components/ApiDesignPanel'
import type { ApiDesign } from '../components/ApiDesignPanel'
import BodyEditor from '../components/BodyEditor'
import type { BodyValue } from '../components/BodyEditor'
import KvEditor from '../components/KvEditor'
import type { Kv } from '../components/KvEditor'
import ResponsePane from '../components/ResponsePane'
import WrapperPreviewModal from '../components/WrapperPreviewModal'
import SplitPane from '../components/SplitPane'
import { METHOD_COLORS, PALETTE } from '../theme'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'
import { schemaToExample } from '../lib/jsonSchema'

// HttpApi 的脚本列（api.ts 类型未含，本地扩展：后端 JSON 列为 [{"lang","source"}]）
interface ScriptRow { lang: string; source: string }
type ApiFull = HttpApi & { pre_scripts?: ScriptRow[]; post_scripts?: ScriptRow[] }

const EMPTY_ROW: Kv = { key: '', value: '' }
const EMPTY_BODY: BodyValue = { contentType: 0, raw: '' }
const EMPTY_DESIGN_STATE: ApiDesign = { params: [], headers: [], cookies: [], requestSchema: null, responseSchema: null }
const scriptOf = (rows?: ScriptRow[]) => rows?.find((s) => s.lang === 'python')?.source ?? ''

// 表单快照（用于 dirty 判定：与"已保存/已回填"时刻的快照对比；含设计态）
const formOf = (
  name: string, method: number, uri: string, params: Kv[], headers: Kv[], cookies: Kv[],
  body: BodyValue, pre: string, post: string, settings: HttpApi['settings'], design: ApiDesign,
) =>
  JSON.stringify({ name, method, uri, params, headers, cookies, body, preScript: pre, postScript: post, settings, design })

const methodOptions = Object.entries(METHOD_COLORS).map(([v, m]) => ({
  value: Number(v),
  label: <span style={{ color: m.color, fontWeight: 700 }}>{m.text}</span>,
}))

// 调试行回传给后端时剥离 enabled（勾选态仅前端语义；protojson 拒绝未知字段）。
// clean 同时过滤掉未勾选与空名行。
const cleanRows = (rows: Kv[]): { key: string; value: string }[] =>
  rows
    .filter((r) => r.key.trim() !== '' && r.enabled !== false)
    .map((r) => ({ key: r.key, value: r.value }))

// API 调试工作区（旗舰页）：/apis/:id 加载已有接口；newMode（/apis 右侧）为空白新建形态。
// createParentId：右键目录「新建接口」进入时的目标目录树节点 id，保存时接口直接落到该目录。
// 「设计」= 类型/必填/默认值/说明预设（存 *_design / *_schema 列）；「调试」= 发请求的运行值，
// 参数/头/Cookie 可勾选是否发送，并支持按设计一键回填默认值。
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
  const [design, setDesign] = useState<ApiDesign>(EMPTY_DESIGN_STATE)
  const [mode, setMode] = useState<'design' | 'debug'>('debug')
  const [savedId, setSavedId] = useState('')
  const [env, setEnv] = useState(envId)
  const [loading, setLoading] = useState(false)
  const [debugResult, setDebugResult] = useState<DebugResult>()
  const [wrapperSource, setWrapperSource] = useState('')
  const [wrapperLoading, setWrapperLoading] = useState(false)
  const [savedSnapshot, setSavedSnapshot] = useState(() => formOf('', 1, '', [EMPTY_ROW], [EMPTY_ROW], [EMPTY_ROW], EMPTY_BODY, '', '', { tls_verify: true, follow_redirects: true, comment_tolerant_json: false }, EMPTY_DESIGN_STATE))
  const sendingRef = useRef(false)
  const apiId = id || savedId

  // 当前接口封装预览（只导出当前接口；保存后可用）
  const previewWrapper = async () => {
    if (!projectId || !apiId) {
      message.warning('当前接口保存后即可查看封装')
      return
    }
    setWrapperLoading(true)
    try {
      const r = await get<{ source: string }>(
        `/api/v1/projects/${projectId}/api-wrappers?http_ids=${apiId}`)
      setWrapperSource(r.source || '# （当前接口暂未生成封装）')
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setWrapperLoading(false)
    }
  }

  // 回填已有接口
  useEffect(() => {
    if (!id || newMode) return
    get<ApiFull>(`/api/v1/apis/${id}`)
      .then((a) => {
        const m = a.method || 1
        const u = a.uri || ''
        const p: Kv[] = a.params?.length ? a.params : [EMPTY_ROW]
        const h: Kv[] = a.headers?.length ? a.headers : [EMPTY_ROW]
        const ck: Kv[] = a.cookies?.length ? a.cookies.map((c) => ({ key: c.name, value: c.value })) : [EMPTY_ROW]
        const b = a.body ?? EMPTY_BODY
        const st = { tls_verify: a.settings?.tls_verify ?? true, follow_redirects: a.settings?.follow_redirects ?? true, comment_tolerant_json: a.settings?.comment_tolerant_json ?? false }
        const pre = scriptOf(a.pre_scripts)
        const post = scriptOf(a.post_scripts)
        const d = designOf(a)
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
        setDesign(d)
        setSavedId(String(a.id))
        setSavedSnapshot(formOf(a.name ?? '', m, u, p, h, ck, b, pre, post, st, d))
      })
      .catch((e) => message.error(e.message))
  }, [id, newMode])

  // 环境：默认跟随全局选择，工作区可临时改
  useEffect(() => setEnv(envId), [envId])

  const currentSnapshot = formOf(name, method, uri, params, headers, cookies, body, preScript, postScript, settings, design)
  const dirty = currentSnapshot !== savedSnapshot
  const { guard, allowOnce } = useLeaveGuard(dirty)

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
        params: cleanRows(params),
        headers: cleanRows(headers),
        cookies: cleanRows(cookies).map((r) => ({ name: r.key, value: r.value })),
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

  // Ctrl/Cmd + Enter 发送（仅调试态；按键定义集中在 src/shortcuts.ts）
  useShortcut(SHORTCUTS.send, send, { enabled: mode === 'debug' })

  const payload = () => {
    const prune = (rows: FieldDesign[]) => rows.filter((r) => r.name.trim() !== '')
    return {
      project_id: projectId,
      name: name.trim(), // 始终发送：清空名称也要能保存（undefined 会被省略 → 后端保留旧值）
      method,
      uri,
      params: cleanRows(params),
      headers: cleanRows(headers),
      cookies: cleanRows(cookies).map((r) => ({ name: r.key, value: r.value })),
      settings,
      body,
      pre_scripts: preScript.trim() ? [{ lang: 'python', source: preScript, enabled: true }] : undefined,
      post_scripts: postScript.trim() ? [{ lang: 'python', source: postScript, enabled: true }] : undefined,
      params_design: prune(design.params),
      headers_design: prune(design.headers),
      cookies_design: prune(design.cookies),
      request_schema: design.requestSchema ?? undefined,
      response_schema: design.responseSchema ?? undefined,
    }
  }

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
      setSavedSnapshot(formOf(saveName, method, uri, params, headers, cookies, body, preScript, postScript, settings, design))
      allowOnce() // 新建成功后直接放行跳转，避免触发未保存确认
      onSaved?.()
      nav(`/apis/${r.id}`)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  // 按设计回填：缺的参数/头/Cookie 行补上（值=默认值→示例值→空），请求体按结构生成示例。
  // 已有行不覆盖值；required 行勾选发送，非必填行也默认勾选（取消勾选即不发送）。
  const syncFromDesign = () => {
    const merge = (rows: Kv[], presets: FieldDesign[]) => {
      const next = rows.filter((r) => r.key.trim() !== '')
      for (const d of presets) {
        if (!d.name || next.some((r) => r.key === d.name)) continue
        next.push({ key: d.name, value: String(d.default ?? d.example ?? ''), enabled: true })
      }
      return next.length ? next : [EMPTY_ROW]
    }
    const nextParams = merge(params, design.params)
    const nextHeaders = merge(headers, design.headers)
    const nextCookies = merge(cookies, design.cookies)
    let nextBody = body
    if (design.requestSchema) {
      const example = JSON.stringify(schemaToExample(design.requestSchema) ?? {}, null, 2)
      if (body.contentType === 0 || !body.raw?.trim()) {
        nextBody = { contentType: 4, raw: example }
      }
    }
    setParams(nextParams)
    setHeaders(nextHeaders)
    setCookies(nextCookies)
    setBody(nextBody)
    if (nextParams === params && nextHeaders === headers && nextCookies === cookies && nextBody === body) {
      message.info('调试参数已与设计一致')
    } else {
      message.success('已按设计回填')
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
          onPressEnter={mode === 'debug' ? send : undefined}
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
        {mode === 'debug' && (
          <Button type="primary" icon={<SendOutlined />} loading={loading} onClick={send}>发送</Button>
        )}
        <Button icon={<SaveOutlined />} onClick={save}>保存</Button>
        {dirty && <Tag color="warning" style={{ marginInlineEnd: 0 }}>未保存</Tag>}
      </div>

      {/* 设计/调试 主页签 */}
      <Tabs
        size="small"
        activeKey={mode}
        onChange={(k) => setMode(k as 'design' | 'debug')}
        style={{ padding: '0 12px', flexShrink: 0 }}
        items={[
          { key: 'debug', label: '调试' },
          { key: 'design', label: '设计' },
        ]}
      />

      {mode === 'design' ? (
        <div style={{ flex: 1, minHeight: 0 }}>
          <ApiDesignPanel design={design} onChange={setDesign} />
        </div>
      ) : (
        // 请求编排 tabs（上部）与响应面板（下部）——可拖拽分栏
        <div style={{ flex: 1, minHeight: 0 }}>
          <SplitPane direction="vertical" initial="45%" min="20%" max="85%">
              <div style={{ height: '100%', overflow: 'auto', padding: '8px 16px' }}>
                <Tabs
              size="small"
              tabBarExtraContent={
                <span style={{ display: 'inline-flex', gap: 4 }}>
                  <Tooltip title="按设计回填：补齐设计里定义的参数/头/Cookie（默认值），请求体按结构生成示例">
                    <Button
                      size="small"
                      type="text"
                      icon={<ThunderboltOutlined />}
                      disabled={design.params.length + design.headers.length + design.cookies.length === 0 && !design.requestSchema}
                      onClick={syncFromDesign}
                    >
                      按设计回填
                    </Button>
                  </Tooltip>
                  <Tooltip title="查看当前接口封装">
                    <Button
                      size="small"
                      type="text"
                      icon={<CodeOutlined />}
                      loading={wrapperLoading}
                      disabled={!apiId}
                      onClick={previewWrapper}
                    />
                  </Tooltip>
                </span>
              }
              items={[
                {
                  key: 'params',
                  label: '参数',
                  children: (
                    <KvEditor
                      value={params} onChange={setParams} checkable
                      meta={designMeta(design.params)}
                    />
                  ),
                },
                {
                  key: 'headers',
                  label: '请求头',
                  children: (
                    <KvEditor
                      value={headers} onChange={setHeaders} checkable
                      meta={designMeta(design.headers)}
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
                      value={cookies} onChange={setCookies} checkable
                      meta={designMeta(design.cookies)}
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
      )}

      <WrapperPreviewModal
        open={!!wrapperSource}
        source={wrapperSource}
        baseUrl={`/api/v1/projects/${projectId}/api-wrappers?http_ids=${apiId}`}
        title={`当前接口封装 · ${name || apiId}`}
        onClose={() => setWrapperSource('')}
      />
      {guard}
    </div>
  )
}
