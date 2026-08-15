import {
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
import type { MenuProps } from 'antd'
import {
  DeleteOutlined, EditOutlined, FolderAddOutlined, FolderOpenOutlined, FolderOutlined,
  MoreOutlined, PlusOutlined,
} from '@ant-design/icons'
import { useEffect, useMemo, useRef, useState } from 'react'
import { del, download, get, post, put, warnTruncated } from '../api'
import type { HttpApi, ListResp, Project, TreeNode } from '../api'
import { PALETTE } from '../theme'
import MethodTag from './MethodTag'
import PanelList from './PanelList'
import { message } from '../messageBridge'

// 接口目录树面板（Apis 页左侧栏）：
// - 树 = 根目录 + 目录嵌套 + 已挂载接口 + 遗留未挂载接口（堆在根末尾）
// - 拖拽用 antd Tree 内置 draggable（原生 HTML5 DnD + 内置插入指示线），
//   落点语义：目录行 上/下=兄弟间插入、中=拖入目录；接口行 上/下=插入（中视为下方）；根行=放入根
// - 树外空白区释放 = 放入根目录末尾
// - 右键菜单（VSCode 式，光标处）：目录处支持「新建接口」落到该目录

interface Props {
  projectId: string
  projects: Project[]
  activeId?: string
  refresh: number // 变化时重载树（ApiDebug 保存后触发）
  onPick: (apiId: string) => void
  onNewApi: (parentId?: string) => void
}

export default function ApiTreePanel({ projectId, projects, activeId, refresh, onPick, onNewApi }: Props) {
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
  // 树空白区右键菜单 + 拖拽悬停空白区的"放入根"阴影
  const [blankMenu, setBlankMenu] = useState<{ x: number; y: number } | null>(null)
  const [rootHover, setRootHover] = useState(false)
  // 拖拽源 key（rc-tree 的 dataTransfer 只写空串，容器级空白区 fallback 需要它）
  const dragNodeRef = useRef<string>('')
  // 空目录"整行拖入"的悬停高亮
  const [intoKey, setIntoKey] = useState('')
  // "插到最前"高亮：展开容器的第一个子节点的顶部半区（内置逻辑会解析成拖入上个容器 → 末尾）
  const [frontKey, setFrontKey] = useState('')
  // frontKey 的持有行（'__root__' 或子节点 key）：enter/leave 乱序到达时，
  // 只有持有行自己的 dragleave 才能清除，避免别行的 leave 误擦指示线
  const frontOwnerRef = useRef('')

  const load = () =>
    Promise.all([
      get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=500`).then((r) => { setRows(r.items); warnTruncated(r, '接口') }),
      get<{ tree: TreeNode[] }>(`/api/v1/tree?project_id=${projectId}`).then((r) => setTree(r.tree)),
    ])
  useEffect(() => {
    setRows([])
    setTree([])
    load().catch((e) => message.error(e.message))
  }, [projectId])
  useEffect(() => {
    load().catch((e) => message.error(e.message))
  }, [refresh])

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

  // 树索引：节点 id → 父节点 id / 接口 id → 节点 id / 父节点 id → 子节点列表
  const nodeMeta = useMemo(() => {
    const parent: Record<string, string> = {}
    const byRef: Record<string, string> = {}
    const children: Record<string, TreeNode[]> = {}
    const walk = (nodes: TreeNode[], p: string) => {
      children[p] = nodes
      for (const n of nodes) {
        parent[n.id] = p
        if (n.ref_id) byRef[n.ref_id] = n.id
        walk(n.children ?? [], n.id)
      }
    }
    walk(tree, '')
    return { parent, byRef, children }
  }, [tree])

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

  const findApiNode = (nodes: TreeNode[], refId: string): TreeNode | undefined => {
    for (const n of nodes) {
      if (n.ref_id === refId) return n
      const hit = n.children && findApiNode(n.children, refId)
      if (hit) return hit
    }
  }

  const submitMove = async () => {
    if (!moveApi) return
    try {
      const node = findApiNode(tree, moveApi.id)
      if (moveTarget === '__unmount__') {
        if (node) await del(`/api/v1/tree/nodes/${node.id}`)
      } else if (moveTarget === '__root__' || moveTarget) {
        const parentId = moveTarget === '__root__' ? 0 : moveTarget
        if (node) {
          await put(`/api/v1/tree/nodes/${node.id}/move`, { parent_id: parentId })
        } else {
          await post('/api/v1/tree/nodes', { project_id: projectId, api_id: moveApi.id, parent_id: parentId })
        }
      }
      message.success('已移动')
      setMoveApi(null)
      load()
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
      if (key === 'new-api') onNewApi(n.id)
      else if (key === 'new-folder') setFolderModal({ mode: 'create', parentId: n.id })
      else if (key === 'rename') { setFolderName(n.name); setFolderModal({ mode: 'rename', node: n }) }
      else if (key === 'del') void removeFolder(n)
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
      if (key === 'move') setMoveApi(a)
      else if (key === 'unmount' && node) {
        del(`/api/v1/tree/nodes/${node.id}`)
          .then(() => { message.success('已从目录移除'); load() })
          .catch((e: any) => message.error(e.message))
      } else if (key === 'del') void remove(a)
    },
  })

  // 根即普通目录：根/空白区菜单同构
  const rootMenuItems: MenuProps['items'] = [
    { key: 'new-api', label: '新建接口', icon: <PlusOutlined /> },
    { key: 'new-folder', label: '新建目录', icon: <FolderAddOutlined /> },
  ]
  const rootMenuClick = ({ key }: { key: string }) => {
    if (key === 'new-api') onNewApi()
    else setFolderModal({ mode: 'create' })
  }

  // 根的第一个子节点 key（根行"插到最前"的目标；空树返回 ''）
  const firstChildKey = (): string => {
    const first = tree[0]
    if (!first) return ''
    return first.node_type === 1 ? `folder-${first.id}` : `api-${first.ref_id}`
  }

  // 根行 = "插到最前"：与首个子节点顶部窄条同语义（统一树顶区域行为），
  // "放入根末尾"由树底部空白区承担
  const rootRowDragOver = (e: any) => {
    const fk = firstChildKey()
    if (!fk) return
    e.preventDefault()
    e.stopPropagation()
    setFrontKey(fk)
    frontOwnerRef.current = '__root__'
  }
  const rootRowDrop = (e: any) => {
    const fk = firstChildKey()
    if (!fk) return
    e.preventDefault()
    e.stopPropagation()
    setFrontKey('')
    frontOwnerRef.current = ''
    const k = dragNodeRef.current
    if (k && k !== fk) void handleDrop(k, fk, -1)
  }

  // ancestorId 的子树是否包含 targetId（防止把目录拖进自己的子孙空目录）
  const isAncestorOf = (ancestorId: string, targetId: string): boolean => {
    const walk = (nodes: TreeNode[]): boolean => {
      for (const node of nodes) {
        if (node.id === ancestorId) {
          const search = (list: TreeNode[]): boolean =>
            list.some((c) => c.id === targetId || search(c.children ?? []))
          return search(node.children ?? [])
        }
        if (walk(node.children ?? [])) return true
      }
      return false
    }
    return walk(tree)
  }

  // 空目录整行 = "拖入"（内置逻辑对无子节点的目录不给 into 落点，这里自定义兜底）
  const dropIntoEmptyFolder = (e: any, n: TreeNode) => {
    e.preventDefault()
    e.stopPropagation()
    setIntoKey(`folder-${n.id}`)
  }
  const dropLeaveEmptyFolder = (e: any) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) setIntoKey('')
  }
  const onDropIntoEmptyFolder = (e: any, n: TreeNode) => {
    e.preventDefault()
    e.stopPropagation()
    setIntoKey('')
    const k = dragNodeRef.current
    if (!k) return
    if (k === `folder-${n.id}`) return // 拖到自身
    if (k.startsWith('folder-') && isAncestorOf(k.slice(7), n.id)) return // 拖入自己的子孙
    setExpandedKeys((prev) => (prev.includes(`folder-${n.id}`) ? prev : [...prev, `folder-${n.id}`]))
    void handleDrop(k, `folder-${n.id}`, 0)
  }

  // 展开容器的第一个子节点（含根）：顶部半区自定义为"插到最前"
  const isFirstVisibleChild = (key: string): boolean => {
    const id = key.startsWith('folder-') ? key.slice(7)
      : key.startsWith('api-') ? nodeMeta.byRef[key.slice(4)] ?? ''
      : ''
    if (!id) return false
    const parentId = nodeMeta.parent[id] ?? ''
    const siblings = nodeMeta.children[parentId] ?? []
    return siblings.length > 0 && siblings[0].id === id
  }

  // dragenter 与 dragover 共用：真实拖拽进入新元素时只触发 dragenter，
  // 只有继续移动才有 dragover —— 只挂 dragover 会导致"停在缝隙上"时不显示指示线
  const rowDragOver = (e: any, key: string) => {
    const el = e.currentTarget as HTMLElement
    if (isFirstVisibleChild(key) && e.clientY < el.getBoundingClientRect().top + el.getBoundingClientRect().height / 2) {
      e.preventDefault()
      e.stopPropagation()
      setFrontKey(key)
      frontOwnerRef.current = key
      return
    }
    setFrontKey('')
    frontOwnerRef.current = ''
    // 其余区域交给内置逻辑
  }

  // 目录行 enter/over 共用：先判"插到最前"（首个子节点顶部半区），再判空目录"整行拖入"
  const folderRowDrag = (e: any, n: TreeNode, isEmpty: boolean) => {
    const key = `folder-${n.id}`
    const el = e.currentTarget as HTMLElement
    if (isFirstVisibleChild(key) && e.clientY < el.getBoundingClientRect().top + el.getBoundingClientRect().height / 2) {
      e.preventDefault()
      e.stopPropagation()
      setFrontKey(key)
      frontOwnerRef.current = key
      return
    }
    setFrontKey('')
    frontOwnerRef.current = ''
    if (isEmpty) dropIntoEmptyFolder(e, n)
  }

  // 只有持有 frontKey 的行自己的 dragleave 才清除（enter/leave 到达顺序不保证）
  const rowDragLeave = (e: any, key: string) => {
    if (frontOwnerRef.current !== key) return
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setFrontKey('')
      frontOwnerRef.current = ''
    }
  }

  const rowDrop = (e: any, key: string) => {
    if (frontKey !== key) return // 非"插到最前"路径，交给内置 drop
    e.preventDefault()
    e.stopPropagation()
    setFrontKey('')
    frontOwnerRef.current = ''
    const k = dragNodeRef.current
    if (k && k !== key) void handleDrop(k, key, -1)
  }

  // ---- 拖拽（antd Tree 内置 draggable）----
  const parseKey = (k: string) =>
    k.startsWith('folder-') ? { kind: 'folder' as const, id: k.slice(7) }
    : k.startsWith('api-') ? { kind: 'api' as const, id: k.slice(4) }
    : { kind: 'root' as const, id: '' }

  // 落点规则：根行只接受"放入"；接口行只接受上/下插入（中会由 rc-tree 回落为"下方"）；目录行全部接受
  const allowDrop = ({ dropNode, dropPosition }: any): boolean => {
    const k = String(dropNode.key)
    if (k === '__root__') return dropPosition === 0
    if (k.startsWith('api-')) return dropPosition !== 0
    return true
  }

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
      load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const onTreeDrop = (info: any) => {
    setRootHover(false)
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

  const apiTitle = (a: HttpApi, subtitle: boolean, dnd?: { key?: string; onDragEnter?: (e: any) => void; onDragOver?: (e: any) => void; onDragLeave?: (e: any) => void; onDrop?: (e: any) => void }) => (
    <div
      className={frontKey === dnd?.key ? 'tp-front-line' : undefined}
      onDragEnter={dnd?.onDragEnter}
      onDragOver={dnd?.onDragOver}
      onDragLeave={dnd?.onDragLeave}
      onDrop={dnd?.onDrop}
      style={{ display: 'flex', alignItems: 'center', gap: 8, paddingRight: 4 }}
    >
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

  const unmounted = filtered.filter((a) => !mountedIds.has(a.id))
  const treeData = useMemo(() => {
    const walk = (nodes: TreeNode[], depth: number): any[] =>
      nodes.map((n) => {
        if (n.node_type === 1) {
          const isEmpty = (n.children ?? []).length === 0
          return {
            key: `folder-${n.id}`,
            className: intoKey === `folder-${n.id}` ? 'tp-into-hover' : undefined,
            title: (
              <Dropdown trigger={['contextMenu']} menu={folderMenu(n)}>
                <div
                  className={frontKey === `folder-${n.id}` ? 'tp-front-line' : undefined}
                  onDoubleClick={(e) => {
                    e.stopPropagation()
                    toggleFolder(`folder-${n.id}`)
                  }}
                  onDragEnter={(e: any) => folderRowDrag(e, n, isEmpty)}
                  onDragOver={(e: any) => folderRowDrag(e, n, isEmpty)}
                  onDragLeave={(e: any) => {
                    rowDragLeave(e, `folder-${n.id}`)
                    if (isEmpty) dropLeaveEmptyFolder(e)
                  }}
                  onDrop={(e: any) => {
                    const key = `folder-${n.id}`
                    if (frontKey === key) {
                      rowDrop(e, key)
                      return
                    }
                    if (isEmpty) onDropIntoEmptyFolder(e, n)
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
                        if (key === 'sub') setFolderModal({ mode: 'create', parentId: n.id })
                        else if (key === 'rename') { setFolderName(n.name); setFolderModal({ mode: 'rename', node: n }) }
                        else removeFolder(n)
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
            children: walk(n.children ?? [], depth + 1),
          }
        }
        const a = rows.find((r) => r.id === n.ref_id)
        if (!a) return null
        return {
          key: `api-${a.id}`,
          title: (
            <Dropdown trigger={['contextMenu']} menu={apiMenu(a, n)}>
              {apiTitle(a, showDetail, {
                key: `api-${a.id}`,
                onDragEnter: (e: any) => rowDragOver(e, `api-${a.id}`),
                onDragOver: (e: any) => rowDragOver(e, `api-${a.id}`),
                onDragLeave: (e: any) => rowDragLeave(e, `api-${a.id}`),
                onDrop: (e: any) => rowDrop(e, `api-${a.id}`),
              })}
            </Dropdown>
          ),
        }
      }).filter(Boolean)
    const folderNodes = walk(tree, 0)
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
            onDragEnter={rootRowDragOver}
            onDragOver={rootRowDragOver}
            onDragLeave={(e: any) => rowDragLeave(e, '__root__')}
            onDrop={rootRowDrop}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              boxShadow: rootHover ? '0 2px 8px rgba(0,0,0,.15)' : undefined,
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
  }, [tree, unmounted, rows, search, projects, projectId, showDetail, rootHover, intoKey, frontKey])

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
              onClick={() => setFolderModal({ mode: 'create' })}
            >
              目录
            </Button>
            <Button
              size="small" icon={<PlusOutlined />}
              onClick={() => onNewApi() /* 顶部新建 = 挂根 */}
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
        className={frontKey || intoKey ? 'tp-custom-drop-active' : undefined}
        style={{ flex: 1, minHeight: 0, overflow: 'auto', padding: '4px 6px' }}
        onDragOver={(e) => {
          if ((e.target as HTMLElement).closest('.ant-tree-treenode')) return // 树内由 rc-tree 处理
          e.preventDefault()
          setRootHover(true) // 空白区 = 放入根目录末尾
        }}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node)) setRootHover(false)
        }}
        onDrop={(e) => {
          e.preventDefault()
          setRootHover(false)
          const k = dragNodeRef.current
          if (k) void handleDrop(k, '__root__', 0)
        }}
        onContextMenu={(e) => {
          if ((e.target as HTMLElement).closest('.ant-tree-treenode')) return // 节点自有菜单
          e.preventDefault()
          setBlankMenu({ x: e.clientX, y: e.clientY }) // 空白区 = 根目录菜单
        }}
      >
        {search.trim() ? (
          <PanelList
            title="搜索结果"
            search=""
            onSearch={() => {}}
            data={filtered}
            activeId={activeId}
            onPick={(a) => onPick(a.id)}
            renderItem={(a) => apiTitle(a, true)}
          />
        ) : (
          <Tree
            showLine={{ showLeafIcon: false }}
            blockNode
            selectedKeys={activeId ? [`api-${activeId}`] : []}
            expandedKeys={expandedKeys}
            onExpand={(keys) => setExpandedKeys(keys)}
            treeData={treeData}
            draggable={{ icon: false, nodeDraggable: (n: any) => String(n.key) !== '__root__' }}
            allowDrop={allowDrop}
            onDrop={onTreeDrop}
            onDragStart={(info: any) => { dragNodeRef.current = String(info.node.key) }}
            onDragEnd={() => { dragNodeRef.current = ''; setRootHover(false); setIntoKey(''); setFrontKey(''); frontOwnerRef.current = '' }}
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
    </div>
  )
}
