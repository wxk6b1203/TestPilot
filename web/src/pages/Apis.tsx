import { Button, Input, Modal, Popconfirm, Space, message } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { del, download, get, post } from '../api'
import type { HttpApi, ListResp } from '../api'
import IdeLayout from '../components/IdeLayout'
import MethodTag from '../components/MethodTag'
import PanelList from '../components/PanelList'
import ApiDebug from './ApiDebug'
import { useLayout } from './Layout'
import { PALETTE } from '../theme'

// 接口工作区：左侧接口列表（搜索/导入/导出）+ 右侧调试区（无选中时为新建/空状态）。
export default function Apis() {
  const { projectId } = useLayout()
  const { id } = useParams() // /apis/:id 时右侧渲染调试区，左侧面板保持
  const nav = useNavigate()
  const [rows, setRows] = useState<HttpApi[]>([])
  const [search, setSearch] = useState('')
  const [newMode, setNewMode] = useState(false)
  const [curlOpen, setCurlOpen] = useState(false)
  const [curlText, setCurlText] = useState('')
  const [oasOpen, setOasOpen] = useState(false)
  const [oasText, setOasText] = useState('')
  const [pmOpen, setPmOpen] = useState(false)
  const [pmText, setPmText] = useState('')
  const [busy, setBusy] = useState('')

  const load = () =>
    projectId
      ? get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=200`).then((r) => setRows(r.items))
      : Promise.resolve()
  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  // 前端按 uri 过滤
  const filtered = useMemo(
    () => (search.trim() ? rows.filter((a) => a.uri.toLowerCase().includes(search.trim().toLowerCase())) : rows),
    [rows, search],
  )

  const remove = async (a: HttpApi) => {
    try {
      await del(`/api/v1/apis/${a.id}`)
      message.success('已删除')
      load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const exportAs = async (kind: 'openapi' | 'postman' | 'curl') => {
    const name = kind === 'postman' ? 'collection.json' : kind === 'curl' ? 'apis.sh' : 'openapi.json'
    try {
      await download(`/api/v1/export/${kind}?project_id=${projectId}`, name)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  // 三个导入 Modal 的公共执行器
  const runImport = async (kind: 'curl' | 'openapi' | 'postman', fn: () => Promise<string>) => {
    setBusy(kind)
    try {
      const msg = await fn()
      message.success(msg)
      load()
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setBusy('')
    }
  }

  const importCurl = () =>
    runImport('curl', async () => {
      await post('/api/v1/import/curl', { project_id: projectId, command: curlText })
      setCurlOpen(false)
      setCurlText('')
      return '已导入'
    })

  const importOas = () =>
    runImport('openapi', async () => {
      // 后端兼容两种形态；JSON/YAML 字符串统一走 document_yaml
      const r = await post<{ created: number; skipped: number }>('/api/v1/import/openapi', {
        project_id: projectId,
        document_yaml: oasText,
      })
      setOasOpen(false)
      setOasText('')
      return `已导入 ${r.created} 个接口，跳过 ${r.skipped} 个`
    })

  const importPm = () => {
    let doc: unknown
    try {
      doc = JSON.parse(pmText)
    } catch {
      message.error('Postman Collection 不是合法 JSON')
      return
    }
    void runImport('postman', async () => {
      await post('/api/v1/import/postman', { project_id: projectId, document: doc })
      setPmOpen(false)
      setPmText('')
      return '已导入'
    })
  }

  if (!projectId)
    return (
      <div style={{
        height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: PALETTE.bgLayout, color: PALETTE.textTertiary,
      }}>
        请先选择项目
      </div>
    )

  const panel = (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ flex: 1, minHeight: 0 }}>
        <PanelList
          title="接口"
          search={search}
          onSearch={setSearch}
          data={filtered}
          activeId={id}
          onPick={(a) => nav(`/apis/${a.id}`)}
          extra={
            <Space size={4}>
              <Button size="small" onClick={() => setCurlOpen(true)}>导入 curl</Button>
              <Button size="small" onClick={() => setOasOpen(true)}>导入 OpenAPI</Button>
              <Button size="small" onClick={() => setPmOpen(true)}>导入 Postman</Button>
            </Space>
          }
          renderItem={(a) => (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <MethodTag method={a.method} />
              <span style={{
                flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                fontSize: 13, color: PALETTE.text,
              }}>
                {a.uri}
              </span>
              <Popconfirm title="删除该接口？" onConfirm={() => remove(a)}>
                <Button
                  type="text" size="small" icon={<DeleteOutlined />}
                  style={{ color: PALETTE.textTertiary }}
                  onClick={(e) => e.stopPropagation()}
                />
              </Popconfirm>
            </div>
          )}
        />
      </div>
      {/* 面板底部：导出链接 */}
      <div style={{
        padding: '6px 10px', borderTop: `1px solid ${PALETTE.border}`,
        display: 'flex', flexWrap: 'wrap', gap: 2, flexShrink: 0,
      }}>
        <Button type="link" size="small" onClick={() => exportAs('openapi')}>导出 OpenAPI</Button>
        <Button type="link" size="small" onClick={() => exportAs('postman')}>导出 Postman</Button>
        <Button type="link" size="small" onClick={() => exportAs('curl')}>导出 curl</Button>
      </div>
    </div>
  )

  const workspace = id ? (
    <ApiDebug key={id} />
  ) : newMode ? (
    <ApiDebug newMode />
  ) : (
    <div style={{
      height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center',
      justifyContent: 'center', gap: 12, background: '#FFFFFF',
    }}>
      <div style={{ color: PALETTE.textTertiary }}>从左侧选择接口，或直接输入 URL 调试</div>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setNewMode(true)}>新建接口</Button>
    </div>
  )

  return (
    <IdeLayout panel={panel}>
      {workspace}

      <Modal
        title="导入 curl"
        open={curlOpen}
        onCancel={() => setCurlOpen(false)}
        onOk={importCurl}
        okText="导入"
        confirmLoading={busy === 'curl'}
        destroyOnHidden
      >
        <Input.TextArea
          rows={8}
          value={curlText}
          onChange={(e) => setCurlText(e.target.value)}
          placeholder={"curl -X POST 'https://api.example.com/users' -H 'Content-Type: application/json' -d '{\"name\": \"neo\"}'"}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
      </Modal>

      <Modal
        title="导入 OpenAPI（JSON / YAML）"
        open={oasOpen}
        onCancel={() => setOasOpen(false)}
        onOk={importOas}
        okText="导入"
        confirmLoading={busy === 'openapi'}
        destroyOnHidden
        width={640}
      >
        <Input.TextArea
          rows={12}
          value={oasText}
          onChange={(e) => setOasText(e.target.value)}
          placeholder={'{\n  "openapi": "3.0.0",\n  "info": {"title": "...", "version": "1.0.0"},\n  "paths": {}\n}'}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
      </Modal>

      <Modal
        title="导入 Postman（Collection v2.1 JSON）"
        open={pmOpen}
        onCancel={() => setPmOpen(false)}
        onOk={importPm}
        okText="导入"
        confirmLoading={busy === 'postman'}
        destroyOnHidden
        width={640}
      >
        <Input.TextArea
          rows={12}
          value={pmText}
          onChange={(e) => setPmText(e.target.value)}
          placeholder={'{\n  "info": {"name": "My Collection", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},\n  "item": []\n}'}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
      </Modal>
    </IdeLayout>
  )
}
