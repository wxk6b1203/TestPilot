import {
  Button, Dropdown, Input, Modal, Select, Space, Tree, message,
} from 'antd'
import {
  EditOutlined, FolderAddOutlined, FolderOpenOutlined, FolderOutlined,
  MoreOutlined, PlusOutlined,
} from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { del, download, get, post, put } from '../api'
import type { HttpApi, ListResp, TreeNode } from '../api'
import IdeLayout from '../components/IdeLayout'
import MethodTag from '../components/MethodTag'
import PanelList from '../components/PanelList'
import ApiDebug from './ApiDebug'
import { useLayout } from './Layout'
import { PALETTE } from '../theme'

// 接口工作区：左侧目录树/列表（搜索时平铺）+ 右侧调试区（无选中时为新建/空状态）。

function isDescendant(nodes: TreeNode[], ancestorId: string, targetId: string): boolean {
  for (const n of nodes) {
    if (n.id === ancestorId) {
      const walk = (list: TreeNode[]): boolean => {
        for (const c of list) {
          if (c.id === targetId) return true
          if (c.children && walk(c.children)) return true
        }
        return false
      }
      return walk(n.children ?? [])
    }
    if (n.children && isDescendant(n.children, ancestorId, targetId)) return true
  }
  return false
}
export default function Apis() {
  const { projectId, projects } = useLayout()
  const { id } = useParams() // /apis/:id 时右侧渲染调试区，左侧面板保持
  const nav = useNavigate()
  const [rows, setRows] = useState<HttpApi[]>([])
  const [tree, setTree] = useState<TreeNode[]>([])
  const [mountedIds, setMountedIds] = useState<Set<string>>(new Set())
  const [search, setSearch] = useState('')
  const [newMode, setNewMode] = useState(false)
  const [curlOpen, setCurlOpen] = useState(false)
  const [curlText, setCurlText] = useState('')
  const [oasOpen, setOasOpen] = useState(false)
  const [oasText, setOasText] = useState('')
  const [pmOpen, setPmOpen] = useState(false)
  const [pmText, setPmText] = useState('')
  const [busy, setBusy] = useState('')
  // 文件夹弹窗：create（parent 可选）/ rename
  const [folderModal, setFolderModal] = useState<{ mode: 'create' | 'rename'; node?: TreeNode; parentId?: string }>()
  const [folderName, setFolderName] = useState('')
  // 接口移动弹窗
  const [moveApi, setMoveApi] = useState<HttpApi | null>(null)
  const [moveTarget, setMoveTarget] = useState<string>('')
  const [expandedKeys, setExpandedKeys] = useState<React.Key[]>(['__root__'])
  const [dropKey, setDropKey] = useState<string>('')

  const load = () =>
    projectId
      ? Promise.all([
          get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=500`).then((r) => setRows(r.items)),
          get<{ tree: TreeNode[] }>(`/api/v1/tree?project_id=${projectId}`).then((r) => setTree(r.tree)),
        ])
      : Promise.resolve()
  useEffect(() => {
    setRows([])
    setTree([])
    load().catch((e) => message.error(e.message))
  }, [projectId])
  useEffect(() => {
    if (id) setNewMode(false)
  }, [id])

  // 收集树中已挂载接口 id
  useEffect(() => {
    const ids = new Set<string>()
    const walk = (nodes: TreeNode[]) => {
      for (const n of nodes) {
        if (n.ref_id) ids.add(n.ref_id)
        if (n.children) walk(n.children)
      }
    }
    walk(tree)
    setMountedIds(ids)
  }, [tree])

  // 前端按 uri/name 过滤（搜索态平铺）
  const filtered = useMemo(
    () => {
      const kw = search.trim().toLowerCase()
      return kw
        ? rows.filter((a) =>
            a.uri.toLowerCase().includes(kw) || (a.name ?? '').toLowerCase().includes(kw))
        : rows
    },
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

  // ---- 目录操作 ----
  const submitFolder = async () => {
    if (!folderName.trim()) {
      message.warning('请输入目录名')
      return
    }
    try {
      if (folderModal?.mode === 'create') {
        await post('/api/v1/tree/folders', {
          project_id: projectId,
          name: folderName.trim(),
          parent_id: folderModal.parentId || undefined,
        })
      } else if (folderModal?.node) {
        await put(`/api/v1/tree/folders/${folderModal.node.id}`, { name: folderName.trim() })
      }
      message.success('已保存')
      setFolderModal(undefined)
      setFolderName('')
      load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const removeFolder = async (n: TreeNode) => {
    try {
      await del(`/api/v1/tree/folders/${n.id}`)
      message.success('已删除（接口仅摘挂）')
      load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const submitMove = async () => {
    if (!moveApi) return
    try {
      if (moveTarget === '__unmount__') {
        // 摘挂：找到挂载节点删除
        const find = (nodes: TreeNode[]): TreeNode | undefined => {
          for (const n of nodes) {
            if (n.ref_id === moveApi.id) return n
            const hit = n.children && find(n.children)
            if (hit) return hit
          }
        }
        const node = find(tree)
        if (node) await del(`/api/v1/tree/nodes/${node.id}`)
      } else if (moveTarget === '__root__' || moveTarget) {
        const find = (nodes: TreeNode[]): TreeNode | undefined => {
          for (const n of nodes) {
            if (n.ref_id === moveApi.id) return n
            const hit = n.children && find(n.children)
            if (hit) return hit
          }
        }
        const node = find(tree)
        if (node) {
          await put(`/api/v1/tree/nodes/${node.id}/move`, {
            parent_id: moveTarget === '__root__' ? 0 : moveTarget,
          })
        } else {
          await post('/api/v1/tree/nodes', {
            project_id: projectId,
            api_id: moveApi.id,
            parent_id: moveTarget === '__root__' ? 0 : moveTarget,
          })
        }
      }
      message.success('已移动')
      setMoveApi(null)
      load()
    } catch (e: any) {
      message.error(e.message)
    }
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

  const findNode = (nodes: TreeNode[], refId: string): TreeNode | undefined => {
    for (const n of nodes) {
      if (n.ref_id === refId) return n
      const hit = n.children && findNode(n.children, refId)
      if (hit) return hit
    }
  }

  // 拖拽/菜单统一移动：nodeKey 形如 api-<id> 或 folder-<id>；parentId 空串=根目录
  const moveNodeTo = async (nodeKey: string, parentId: string) => {
    try {
      if (nodeKey.startsWith('folder-')) {
        await put(`/api/v1/tree/nodes/${nodeKey.slice(7)}/move`, { parent_id: parentId || 0 })
      } else if (nodeKey.startsWith('api-')) {
        const aid = nodeKey.slice(4)
        const node = findNode(tree, aid)
        if (node) {
          await put(`/api/v1/tree/nodes/${node.id}/move`, { parent_id: parentId || 0 })
        } else if (parentId) {
          await post('/api/v1/tree/nodes', { project_id: projectId, api_id: aid, parent_id: parentId })
        }
      }
      load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  // 文件夹下拉（移动目标）
  const folderOptions = useMemo(() => {
    const out: { value: string; label: string }[] = [{ value: '__root__', label: '（根目录）' }, { value: '__unmount__', label: '（未分类）' }]
    const walk = (nodes: TreeNode[], depth: number) => {
      for (const n of nodes) {
        if (n.node_type === 1) {
          out.push({ value: n.id, label: `${'  '.repeat(depth)}📁 ${n.name}` })
          if (n.children) walk(n.children, depth + 1)
        }
      }
    }
    walk(tree, 0)
    return out
  }, [tree])

  const apiTitle = (a: HttpApi, subtitle = true) => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, paddingRight: 4 }}>
      <MethodTag method={a.method} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          fontSize: 13, color: PALETTE.text,
        }}>
          {a.name || a.uri}
        </div>
        {subtitle && a.name && (
          <div style={{ fontSize: 11, color: PALETTE.textTertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {a.uri}
          </div>
        )}
      </div>
      <Dropdown
        trigger={['click']}
        menu={{
          items: [
            { key: 'move', label: '移动到目录…' },
            { key: 'del', label: '删除接口', danger: true },
          ],
          onClick: ({ key }) => {
            if (key === 'move') setMoveApi(a)
            else remove(a)
          },
        }}
      >
        <Button type="text" size="small" icon={<MoreOutlined />}
          style={{ color: PALETTE.textTertiary }}
          onClick={(e) => e.stopPropagation()} />
      </Dropdown>
    </div>
  )

  // 树数据：folder 树 + 「（未分类）」虚拟根
  const unmounted = filtered.filter((a) => !mountedIds.has(a.id))
  const treeData = useMemo(() => {
    const walk = (nodes: TreeNode[]): any[] =>
      nodes.map((n) => {
        if (n.node_type === 1) {
          return {
            key: `folder-${n.id}`,
            title: (
              <div style={{
                display: 'flex', alignItems: 'center', gap: 6, paddingRight: 4,
                ...(dropKey === `folder-${n.id}` ? {
                  background: 'rgba(82,196,26,.12)',
                  boxShadow: 'inset 0 0 0 1px rgba(82,196,26,.34)',
                  borderRadius: 6,
                } : {}),
              }}>
                <FolderOutlined style={{ color: PALETTE.primary, fontSize: 14 }} />
                <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
                  {n.name}
                </span>
                <Dropdown
                  trigger={['click']}
                  menu={{
                    items: [
                      { key: 'sub', label: '新建子目录', icon: <FolderAddOutlined /> },
                      { key: 'rename', label: '重命名', icon: <EditOutlined /> },
                      { key: 'del', label: '删除目录', danger: true },
                    ],
                    onClick: ({ key }) => {
                      if (key === 'sub') setFolderModal({ mode: 'create', parentId: n.id })
                      else if (key === 'rename') { setFolderName(n.name); setFolderModal({ mode: 'rename', node: n }) }
                      else removeFolder(n)
                    },
                  }}
                >
                  <Button type="text" size="small" icon={<MoreOutlined />}
                    style={{ color: PALETTE.textTertiary }}
                    onClick={(e) => e.stopPropagation()} />
                </Dropdown>
              </div>
            ),
            selectable: false,
            children: walk(n.children ?? []),
          }
        }
        const a = rows.find((r) => r.id === n.ref_id)
        if (!a) return null
        return {
          key: `api-${a.id}`,
          title: apiTitle(a, search.trim() === ''),
        }
      }).filter(Boolean)
    const folderNodes = walk(tree)
    if (unmounted.length && search.trim() === '') {
      folderNodes.push(...unmounted.map((a) => ({
        key: `api-${a.id}`,
        title: apiTitle(a, false),
      })))
    }
    const rootName = projects.find((p) => p.id === projectId)?.name || '根目录'
    return [{
      key: '__root__',
      title: (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          ...(dropKey === '__root__' ? {
            background: 'rgba(82,196,26,.12)',
            boxShadow: 'inset 0 0 0 1px rgba(82,196,26,.34)',
            borderRadius: 6,
          } : {}),
        }}>
          <FolderOpenOutlined style={{ color: PALETTE.primary, fontSize: 14 }} />
          <span style={{ fontSize: 13, fontWeight: 600, color: PALETTE.text }}>{rootName}</span>
        </div>
      ),
      selectable: false,
      children: folderNodes,
    }]
  }, [tree, unmounted, rows, search, projects, projectId, dropKey])

  const panel = (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ padding: '10px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>接口</span>
          <Space size={4}>
            <Button
              size="small" type="primary" icon={<FolderAddOutlined />}
              onClick={() => setFolderModal({ mode: 'create' })}
            >
              目录
            </Button>
            <Button
              size="small" icon={<PlusOutlined />}
              onClick={() => {
                setNewMode(true)
                nav('/apis') // 清掉 :id，让工作区切到新建形态
              }}
            >
              接口
            </Button>
          </Space>
        </div>
        <Input
          size="small" allowClear
          placeholder="搜索（名称 / uri）"
          value={search} onChange={(e) => setSearch(e.target.value)}
        />
      </div>
      <div style={{ flex: 1, minHeight: 0, overflow: 'auto', padding: '4px 6px' }}>
        {search.trim() ? (
          <PanelList
            title="搜索结果"
            search=""
            onSearch={() => {}}
            data={filtered}
            activeId={id}
            onPick={(a) => nav(`/apis/${a.id}`)}
            renderItem={(a) => apiTitle(a, true)}
          />
        ) : (
          <Tree
            showLine={{ showLeafIcon: false }}
            blockNode
            draggable={{ icon: false }}
            selectedKeys={id ? [`api-${id}`] : []}
            expandedKeys={expandedKeys}
            onExpand={(keys) => setExpandedKeys(keys)}
            treeData={treeData}
            onSelect={(keys) => {
              const k = String(keys[0] ?? '')
              if (k.startsWith('api-')) nav(`/apis/${k.slice(4)}`)
            }}
            onDragEnter={({ node }) => {
              const k = String(node.key)
              setDropKey(k)
              // 悬停目录自动展开（file-browser hover-expand）
              if (k.startsWith('folder-')) {
                setExpandedKeys((prev) => (prev.includes(k) ? prev : [...prev, k]))
              }
            }}
            onDragOver={() => {}}
            onDragLeave={() => setDropKey('')}
            onDrop={(info) => {
              setDropKey('')
              const dragKey = String(info.dragNode.key)
              const dropKey = String(info.node.key)
              // 拖到空白/根节点 → 移动到根（顶层）；拖入目录 → 进目录
              if (info.dropToGap || dropKey === '__root__') {
                void moveNodeTo(dragKey, '')
                return
              }
              if (!dropKey.startsWith('folder-')) {
                message.warning('请拖入目录（拖到空白处移动到根目录）')
                return
              }
              if (dragKey === dropKey || (dragKey.startsWith('folder-') && isDescendant(tree, dragKey.slice(7), dropKey.slice(7)))) {
                message.warning('不能移动到自身或其子目录')
                return
              }
              void moveNodeTo(dragKey, dropKey.slice(7))
            }}
          />
        )}
      </div>
      {/* 面板底部：导入/导出 */}
      <div style={{
        padding: '6px 10px', borderTop: `1px solid ${PALETTE.border}`,
        display: 'flex', flexWrap: 'wrap', gap: 2, flexShrink: 0,
      }}>
        <Button size="small" onClick={() => setCurlOpen(true)}>导入 curl</Button>
        <Button size="small" onClick={() => setOasOpen(true)}>导入 OpenAPI</Button>
        <Button size="small" onClick={() => setPmOpen(true)}>导入 Postman</Button>
        <Button type="link" size="small" onClick={() => exportAs('openapi')}>导出 OpenAPI</Button>
        <Button type="link" size="small" onClick={() => exportAs('postman')}>导出 Postman</Button>
        <Button type="link" size="small" onClick={() => exportAs('curl')}>导出 curl</Button>
      </div>
    </div>
  )

  const workspace = id ? (
    <ApiDebug key={id} onSaved={() => { void load() }} />
  ) : newMode ? (
    <ApiDebug newMode onSaved={() => { void load() }} />
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

      <Modal
        title={folderModal?.mode === 'rename' ? '重命名目录' : '新建目录'}
        open={!!folderModal}
        onCancel={() => setFolderModal(undefined)}
        onOk={submitFolder}
        okText="保存"
        destroyOnHidden
      >
        <Input
          value={folderName}
          onChange={(e) => setFolderName(e.target.value)}
          placeholder="目录名"
          onPressEnter={submitFolder}
        />
      </Modal>

      <Modal
        title={moveApi ? `移动「${moveApi.name || moveApi.uri}」` : ''}
        open={!!moveApi}
        onCancel={() => setMoveApi(null)}
        onOk={submitMove}
        okText="移动"
        destroyOnHidden
      >
        <Select
          style={{ width: '100%' }}
          value={moveTarget}
          onChange={setMoveTarget}
          options={folderOptions}
          placeholder="选择目标目录"
        />
      </Modal>
    </IdeLayout>
  )
}
