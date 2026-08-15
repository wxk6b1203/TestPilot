import {
  App as AntdApp,
  Button,
  Dropdown,
  Input,
  Menu,
  Modal,
  Select,
  Space,
  Switch,
  Tree,
} from 'antd'
import type { MenuProps, TreeProps } from 'antd'
import {
  DeleteOutlined, EditOutlined, FolderAddOutlined, FolderOpenOutlined, FolderOutlined,
  MoreOutlined, PlusOutlined,
} from '@ant-design/icons'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { del, download, get, post, put } from '../api'
import type { HttpApi, ListResp, Project, TreeNode } from '../api'
import { PALETTE } from '../theme'
import MethodTag from './MethodTag'
import PanelList from './PanelList'
import { message } from '../messageBridge'

// 接口目录树面板（Apis 页左侧栏）：
// - 树 = 根目录 + 目录嵌套 + 已挂载接口 + 遗留未挂载接口（堆在根末尾）
// - 拖拽使用 antd 6.6.0 Tree 原生 draggable/onDrop 行为（内置 DropIndicator，无自定义阴影/空白区拖放）
// - 右键菜单（VSCode 式，光标处）：目录处支持「新建接口」落到该目录

interface Props {
  projectId: string
  projects: Project[]
  activeId?: string
  refresh: number // 变化时重载树（ApiDebug 保存后触发）
  onPick: (apiId: string) => void
  onNewApi: (parentId?: string) => void
  // 删除当前正在打开的接口时通知页面清掉 /apis/:id，避免右侧继续编辑已删除数据
  onDeleted?: (apiId: string) => void
}

export default function ApiTreePanel({ projectId, projects, activeId, refresh, onPick, onNewApi, onDeleted }: Props) {
  const { modal } = AntdApp.useApp()
  const [rows, setRows] = useState<HttpApi[]>([])
  const [tree, setTree] = useState<TreeNode[]>([])
  const [search, setSearch] = useState('')
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
  const [showDetail, setShowDetail] = useState(false) // 默认简略
  // 树空白区右键菜单（与拖拽无关）
  const [blankMenu, setBlankMenu] = useState<{ x: number; y: number } | null>(null)

  // 拉取数据：接口列表按 500/页循环取全量，避免截断导致树节点/搜索静默缺失。
  const fetchData = useCallback(async () => {
    const fetchAllApis = async (): Promise<HttpApi[]> => {
      const out: HttpApi[] = []
      const seen = new Set<string>()
      let page = 1
      let total = 0
      do {
        const r = await get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page=${page}&page_size=500`)
        total = r.total
        for (const item of r.items) {
          if (!seen.has(item.id)) {
            seen.add(item.id)
            out.push(item)
          }
        }
        if (r.items.length === 0) break
        page += 1
      } while (out.length < total)
      return out
    }
    const [apiItems, treeRes] = await Promise.all([
      fetchAllApis(),
      get<{ tree: TreeNode[] }>(`/api/v1/tree?project_id=${projectId}`),
    ])
    return { apiItems, tree: treeRes.tree }
  }, [projectId])

  // 所有重载统一入口：内部捕获错误；seq 丢弃过期响应（快速切项目时防止旧数据覆盖新项目）。
  const reloadSeqRef = useRef(0)
  const reload = useCallback(async () => {
    const seq = ++reloadSeqRef.current
    try {
      const data = await fetchData()
      if (seq !== reloadSeqRef.current) return
      setRows(data.apiItems)
      setTree(data.tree)
    } catch (e: any) {
      if (seq !== reloadSeqRef.current) return
      message.error(e.message)
    }
  }, [fetchData])

  // 首次挂载只发一次请求；之后 projectId/refresh 变化各触发一次，互不重复。
  const initialLoadRef = useRef(false)
  const prevProjectRef = useRef(projectId)
  const prevRefreshRef = useRef(refresh)
  useEffect(() => {
    const first = !initialLoadRef.current
    const projectChanged = prevProjectRef.current !== projectId
    const refreshChanged = prevRefreshRef.current !== refresh
    if (!first) {
      prevProjectRef.current = projectId
      prevRefreshRef.current = refresh
    } else {
      initialLoadRef.current = true
    }
    if (first || projectChanged) {
      setRows([])
      setTree([])
    }
    if (first || projectChanged || refreshChanged) {
      void reload()
    }
  }, [projectId, refresh, reload])

  // 已挂载接口 id：与 tree 同帧派生，避免同一接口在树内与未分类区并存
  const mountedIds = useMemo(() => {
    const ids = new Set<string>()
    const walk = (nodes: TreeNode[]) => {
      for (const n of nodes) {
        if (n.ref_id) ids.add(n.ref_id)
        if (n.children) walk(n.children)
      }
    }
    walk(tree)
    return ids
  }, [tree])

  // 树索引：节点 id → 节点/父节点 id / 接口 id → 节点 id / 父节点 id → 子节点列表
  const nodeMeta = useMemo(() => {
    const byId: Record<string, TreeNode> = {}
    const parent: Record<string, string> = {}
    const byRef: Record<string, string> = {}
    const children: Record<string, TreeNode[]> = {}
    const walk = (nodes: TreeNode[], p: string) => {
      children[p] = nodes
      for (const n of nodes) {
        byId[n.id] = n
        parent[n.id] = p
        if (n.ref_id) byRef[n.ref_id] = n.id
        walk(n.children ?? [], n.id)
      }
    }
    walk(tree, '')
    return { byId, parent, byRef, children }
  }, [tree])

  // rows 的 id 索引：树构建从 O(n*m) 降到 O(n)
  const rowsById = useMemo(() => {
    const map = new Map<string, HttpApi>()
    for (const a of rows) map.set(a.id, a)
    return map
  }, [rows])

  const filtered = useMemo(() => {
    const kw = search.trim().toLowerCase()
    return kw
      ? rows.filter((a) => a.uri.toLowerCase().includes(kw) || (a.name ?? '').toLowerCase().includes(kw))
      : rows
  }, [rows, search])

  const remove = async (a: HttpApi) => {
    try {
      await del(`/api/v1/apis/${a.id}`)
      message.success('已删除')
      onDeleted?.(a.id)
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  // 危险操作二次确认（目录删除会级联删除子目录）
  const confirmRemoveApi = (a: HttpApi) => {
    modal.confirm({
      title: `删除接口「${a.name || a.uri}」？`,
      content: '删除后不可恢复，相关目录挂载会一并移除。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => remove(a),
    })
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
      void reload()
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
  // 新建目录统一入口：先清空上次可能残留的目录名（重命名取消后不能串到新建弹窗）
  const openFolderCreate = (parentId?: string) => {
    setFolderName('')
    setFolderModal({ mode: 'create', parentId })
    const targetKey = parentId ? `folder-${parentId}` : '__root__'
    setExpandedKeys((prev) => (prev.includes(targetKey) ? prev : [...prev, targetKey]))
  }
  const openFolderRename = (n: TreeNode) => {
    setFolderName(n.name)
    setFolderModal({ mode: 'rename', node: n })
  }

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
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const removeFolder = async (n: TreeNode) => {
    try {
      await del(`/api/v1/tree/folders/${n.id}`)
      message.success('目录及子目录已删除（接口仅摘挂）')
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const confirmRemoveFolder = (n: TreeNode) => {
    modal.confirm({
      title: `删除目录「${n.name}」？`,
      content: '目录及所有子目录会被删除；目录中的接口仅摘挂，接口本身不会被删除。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => removeFolder(n),
    })
  }

  // 从目录摘挂（不删除接口）
  const unmountApi = (node: TreeNode) => {
    del(`/api/v1/tree/nodes/${node.id}`)
      .then(() => { message.success('已从目录移除'); void reload() })
      .catch((e: any) => message.error(e.message))
  }

  // 打开移动弹窗时重置目标，避免沿用上一次的选择造成误移动
  const openMove = (a: HttpApi) => {
    setMoveTarget('')
    setMoveApi(a)
  }

  const submitMove = async () => {
    if (!moveApi) return
    if (!moveTarget) {
      message.warning('请选择目标目录')
      return
    }
    const nodeId = nodeMeta.byRef[moveApi.id]
    try {
      if (moveTarget === '__unmount__') {
        if (!nodeId) {
          message.info('该接口已在未分类中')
          setMoveTarget('')
          setMoveApi(null)
          return
        }
        await del(`/api/v1/tree/nodes/${nodeId}`)
      } else {
        const targetParentId = moveTarget === '__root__' ? '' : moveTarget
        if (nodeId && (nodeMeta.parent[nodeId] ?? '') === targetParentId) {
          message.info('接口已在该目录中，无需移动')
          setMoveTarget('')
          setMoveApi(null)
          return
        }
        const parentId = moveTarget === '__root__' ? 0 : moveTarget
        if (nodeId) {
          await put(`/api/v1/tree/nodes/${nodeId}/move`, { parent_id: parentId })
        } else {
          await post('/api/v1/tree/nodes', { project_id: projectId, api_id: moveApi.id, parent_id: parentId })
        }
      }
      message.success('已移动')
      setMoveTarget('')
      setMoveApi(null)
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  // ---- 右键菜单（VSCode 式：节点处 Dropdown trigger=contextMenu，菜单出现在光标处）----
  const folderMenu = (n: TreeNode): MenuProps => ({
    items: [
      { key: 'new-api', label: '新建接口', icon: <PlusOutlined /> },
      { key: 'new-folder', label: '新建子目录', icon: <FolderAddOutlined /> },
      { type: 'divider' },
      { key: 'rename', label: '重命名', icon: <EditOutlined /> },
      { type: 'divider' },
      { key: 'del', label: '删除目录', icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'new-api') {
        setExpandedKeys((prev) => (prev.includes(`folder-${n.id}`) ? prev : [...prev, `folder-${n.id}`]))
        onNewApi(n.id)
      } else if (key === 'new-folder') openFolderCreate(n.id)
      else if (key === 'rename') openFolderRename(n)
      else if (key === 'del') confirmRemoveFolder(n)
    },
  })

  const apiMenu = (a: HttpApi, node?: TreeNode): MenuProps => ({
    items: [
      { key: 'move', label: '移动到目录…' },
      ...(node ? [{ key: 'unmount', label: '从目录移除' }] : []),
      { type: 'divider' },
      { key: 'del', label: '删除接口', icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'move') openMove(a)
      else if (key === 'unmount' && node) unmountApi(node)
      else if (key === 'del') confirmRemoveApi(a)
    },
  })

  // 根目录新建接口：确保根展开，保存后新节点可见
  const openNewApiAtRoot = () => {
    setExpandedKeys((prev) => (prev.includes('__root__') ? prev : [...prev, '__root__']))
    onNewApi()
  }

  // 根即普通目录：根/空白区菜单同构
  const rootMenuItems: MenuProps['items'] = [
    { key: 'new-api', label: '新建接口', icon: <PlusOutlined /> },
    { key: 'new-folder', label: '新建目录', icon: <FolderAddOutlined /> },
  ]
  const rootMenuClick = ({ key }: { key: string }) => {
    if (key === 'new-api') openNewApiAtRoot()
    else openFolderCreate()
  }

  // ---- 拖拽：使用 antd 6.6.0 Tree 原生 draggable + onDrop 语义 ----
  const parseKey = (k: string) =>
    k.startsWith('folder-') ? { kind: 'folder' as const, id: k.slice(7) }
    : k.startsWith('api-') ? { kind: 'api' as const, id: k.slice(4) }
    : { kind: 'root' as const, id: '' }

  const handleDrop = async (dragKey: string, dropKey: string, dropPos: number) => {
    const d = parseKey(dragKey)
    const t = parseKey(dropKey)
    // 目标：父目录 + 插入位置（null = 追加末尾）
    let parentId = ''
    let index: number | null = null
    if (t.kind === 'root') {
      parentId = ''
    } else if (t.kind === 'folder') {
      if (dropPos === 0) {
        parentId = t.id // 拖入目录 → 末尾
      } else {
        parentId = nodeMeta.parent[t.id] ?? ''
        const siblings = nodeMeta.children[parentId] ?? []
        index = siblings.findIndex((c) => c.id === t.id) + (dropPos > 0 ? 1 : 0)
      }
    } else {
      // 接口行：0 视为放其下方
      const nodeId = nodeMeta.byRef[t.id]
      if (nodeId) {
        parentId = nodeMeta.parent[nodeId] ?? ''
        const siblings = nodeMeta.children[parentId] ?? []
        index = siblings.findIndex((c) => c.id === nodeId) + (dropPos >= 0 ? 1 : 0)
      }
      // 遗留未挂载接口：挂到根末尾
    }
    const draggedNodeId = d.kind === 'folder' ? d.id : nodeMeta.byRef[d.id] ?? ''
    try {
      if (!draggedNodeId) {
        // 未挂载接口 → 挂载到目标位置
        await post('/api/v1/tree/nodes', {
          project_id: projectId, api_id: d.id, parent_id: parentId || 0,
          index: index ?? undefined,
        })
      } else if ((nodeMeta.parent[draggedNodeId] ?? '') === parentId) {
        // 同父 → 前端重算顺序走 reorder
        const siblings = nodeMeta.children[parentId] ?? []
        const ids = siblings.map((s) => s.id).filter((id) => id !== draggedNodeId)
        let at = index ?? ids.length
        ids.splice(at, 0, draggedNodeId)
        await put('/api/v1/tree/reorder', { parent_id: parentId || 0, ids })
      } else {
        // 跨目录 → move + 精确插入位置
        await put(`/api/v1/tree/nodes/${draggedNodeId}/move`, {
          parent_id: parentId || 0, index: index ?? undefined,
        })
      }
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const onTreeDrop: TreeProps['onDrop'] = (info) => {
    const dragKey = String(info.dragNode.key)
    const dropKey = String(info.node.key)
    const posArr = String(info.node.pos).split('-')
    const dropPos = info.dropPosition - Number(posArr[posArr.length - 1]) // 归一化 -1/0/1
    void handleDrop(dragKey, dropKey, dropPos)
  }

  // 双击目录折叠/展开
  const toggleFolder = (key: string) => {
    setExpandedKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]))
  }

  // 文件夹下拉（移动目标）：禁用当前所在目录；未挂载接口禁用“未分类”
  const folderOptions = useMemo(() => {
    const moveNodeId = moveApi ? nodeMeta.byRef[moveApi.id] : undefined
    const currentParentId = moveNodeId ? nodeMeta.parent[moveNodeId] ?? '' : null
    const isCurrent = (value: string) => currentParentId !== null && (value === '__root__' ? '' : value) === currentParentId
    const out: { value: string; label: string; disabled?: boolean }[] = [
      { value: '__root__', label: '（根目录）', disabled: isCurrent('__root__') },
      { value: '__unmount__', label: '（未分类）', disabled: !!moveApi && !moveNodeId },
    ]
    const walk = (nodes: TreeNode[], depth: number) => {
      for (const n of nodes) {
        if (n.node_type === 1) {
          out.push({ value: n.id, label: `${'  '.repeat(depth)}📁 ${n.name}`, disabled: isCurrent(n.id) })
          if (n.children) walk(n.children, depth + 1)
        }
      }
    }
    walk(tree, 0)
    return out
  }, [tree, moveApi, nodeMeta])

  const apiTitle = (a: HttpApi, subtitle: boolean, node?: TreeNode) => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, paddingRight: 4 }}>
      <MethodTag method={a.method} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          fontSize: 13, color: PALETTE.text,
        }}>
          {subtitle ? (a.name || '未命名') : (a.name || a.uri)}
        </div>
        {subtitle && (
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
            ...(node ? [{ key: 'unmount', label: '从目录移除' }] : []),
            { key: 'del', label: '删除接口', danger: true },
          ],
          onClick: ({ key }) => {
            if (key === 'move') openMove(a)
            else if (key === 'unmount' && node) unmountApi(node)
            else if (key === 'del') confirmRemoveApi(a)
          },
        }}
      >
        <Button type="text" size="small" icon={<MoreOutlined />}
          style={{ color: PALETTE.textTertiary }}
          onClick={(e) => e.stopPropagation()} />
      </Dropdown>
    </div>
  )

  const unmounted = useMemo(
    () => filtered.filter((a) => !mountedIds.has(a.id)),
    [filtered, mountedIds],
  )
  // 每次渲染直接构建 treeData。之前 useMemo 因 unmounted 引用不稳定实际每帧都重建，
  // 还引入了一组缺失依赖的告警；这里改为显式每帧构建。
  const treeData = (() => {
    const walk = (nodes: TreeNode[]): any[] =>
      nodes.map((n) => {
        if (n.node_type === 1) {
          return {
            key: `folder-${n.id}`,
            title: (
              <Dropdown trigger={['contextMenu']} menu={folderMenu(n)}>
                <div
                  onDoubleClick={(e) => {
                    e.stopPropagation()
                    toggleFolder(`folder-${n.id}`)
                  }}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 6, paddingRight: 4,
                    borderRadius: 6,
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
                        if (key === 'sub') openFolderCreate(n.id)
                        else if (key === 'rename') openFolderRename(n)
                        else confirmRemoveFolder(n)
                      },
                    }}
                  >
                    <Button type="text" size="small" icon={<MoreOutlined />}
                      style={{ color: PALETTE.textTertiary }}
                      onClick={(e) => e.stopPropagation()}
                      onDoubleClick={(e) => e.stopPropagation()} />
                  </Dropdown>
                </div>
              </Dropdown>
            ),
            selectable: false,
            children: walk(n.children ?? []),
          }
        }
        const refId = n.ref_id ?? n.ref?.id ?? ''
        const a = rowsById.get(refId) ?? (
          n.ref
            ? { id: refId, method: n.ref.method, uri: n.ref.uri, name: n.ref.name } as HttpApi
            : undefined
        )
        if (!a) return null
        return {
          key: `api-${a.id}`,
          title: (
            <Dropdown trigger={['contextMenu']} menu={apiMenu(a, n)}>
              {apiTitle(a, showDetail, n)}
            </Dropdown>
          ),
        }
      }).filter(Boolean)
    const folderNodes = walk(tree)
    if (unmounted.length && search.trim() === '') {
      folderNodes.push(...unmounted.map((a) => ({
        key: `api-${a.id}`,
        title: (
          <Dropdown trigger={['contextMenu']} menu={apiMenu(a)}>
            {apiTitle(a, showDetail)}
          </Dropdown>
        ),
      })))
    }
    const rootName = projects.find((p) => p.id === projectId)?.name || '根目录'
    return [{
      key: '__root__',
      title: (
        <Dropdown trigger={['contextMenu']} menu={{ items: rootMenuItems, onClick: rootMenuClick }}>
          <div
            onDoubleClick={(e) => {
              e.stopPropagation()
              toggleFolder('__root__')
            }}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              borderRadius: 6,
            }}
          >
            <FolderOpenOutlined style={{ color: PALETTE.primary, fontSize: 14 }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: PALETTE.text }}>{rootName}</span>
          </div>
        </Dropdown>
      ),
      selectable: false,
      children: folderNodes,
    }]
  })()

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ padding: '10px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <Space size={8}>
            <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>接口</span>
            <Switch
              size="small"
              checked={showDetail}
              onChange={setShowDetail}
              checkedChildren="详细"
              unCheckedChildren="简略"
            />
          </Space>
          <Space size={4}>
            <Button
              size="small" type="primary" icon={<FolderAddOutlined />}
              onClick={() => openFolderCreate()}
            >
              目录
            </Button>
            <Button
              size="small" icon={<PlusOutlined />}
              onClick={openNewApiAtRoot /* 顶部新建 = 挂根 */}
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
      <div
        style={{ flex: 1, minHeight: 0, overflow: 'auto', padding: '4px 6px' }}
        onContextMenu={(e) => {
          if (search.trim()) {
            // 搜索模式没有树空白区，结果行有自己的右键菜单；这里不再弹根目录菜单
            e.preventDefault()
            return
          }
          if ((e.target as HTMLElement).closest('.ant-tree-treenode')) return // 节点自有菜单
          e.preventDefault()
          setBlankMenu({ x: e.clientX, y: e.clientY }) // 空白区 = 根目录菜单
        }}
      >
        {search.trim() ? (
          <PanelList
            title="搜索结果"
            hideSearch
            data={filtered}
            activeId={activeId}
            onPick={(a) => onPick(a.id)}
            renderItem={(a) => {
              const nodeId = nodeMeta.byRef[a.id]
              const node = nodeId ? nodeMeta.byId[nodeId] : undefined
              return (
                <Dropdown trigger={['contextMenu']} menu={apiMenu(a, node)}>
                  <div>{apiTitle(a, true, node)}</div>
                </Dropdown>
              )
            }}
          />
        ) : (
          <Tree
            showLine={{ showLeafIcon: false }}
            blockNode
            selectedKeys={activeId ? [`api-${activeId}`] : []}
            expandedKeys={expandedKeys}
            onExpand={(keys) => setExpandedKeys(keys)}
            treeData={treeData}
            draggable={{ icon: false, nodeDraggable: (node) => String(node.key) !== '__root__' }}
            onDrop={onTreeDrop}
            onSelect={(keys) => {
              const k = String(keys[0] ?? '')
              if (k.startsWith('api-')) onPick(k.slice(4))
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

      {/* 树空白区右键菜单：手工定位（节点处菜单由各自 Dropdown 承载） */}
      {blankMenu && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 1000 }}
            onClick={() => setBlankMenu(null)}
            onContextMenu={(e) => { e.preventDefault(); setBlankMenu(null) }}
          />
          <Menu
            style={{
              position: 'fixed', left: blankMenu.x, top: blankMenu.y, zIndex: 1001,
              minWidth: 140, boxShadow: '0 6px 16px rgba(0,0,0,.12)', borderRadius: 8, padding: 4,
            }}
            items={rootMenuItems}
            onClick={({ key }) => { rootMenuClick({ key }); setBlankMenu(null) }}
          />
        </>
      )}

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
        onCancel={() => { setFolderModal(undefined); setFolderName('') }}
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
        onCancel={() => { setMoveApi(null); setMoveTarget('') }}
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
          showSearch
          optionFilterProp="label"
        />
      </Modal>
    </div>
  )
}
