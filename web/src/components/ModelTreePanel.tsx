import { App as AntdApp, Button, Dropdown, Input, Menu, Modal, Space, Tree } from 'antd'
import type { MenuProps, TreeProps } from 'antd'
import {
  ApartmentOutlined, DeleteOutlined, EditOutlined, FolderAddOutlined,
  FolderOpenOutlined, FolderOutlined, MenuFoldOutlined, MoreOutlined, PlusOutlined,
} from '@ant-design/icons'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { del, get, post, put } from '../api'
import type { DataModel, ListResp, Project, TreeNode } from '../api'
import { PALETTE } from '../theme'
import { message } from '../messageBridge'

// 数据模型（结构）目录树面板：位于接口树下方。数据/交互与 ApiTreePanel 同构
// （kind=models 的树 + 未挂载模型平铺根尾 + 拖拽/右键管理），但更精简：
// 无搜索/详情开关/导入导出，右键直达建模操作。

interface Props {
  projectId: string
  projects: Project[]
  activeId?: string
  refresh: number
  onPick: (modelId: string) => void
  onNewModel: (parentId?: string) => void
  onDeleted?: (modelId: string) => void
}

export default function ModelTreePanel({ projectId, projects, activeId, refresh, onPick, onNewModel, onDeleted }: Props) {
  const { modal } = AntdApp.useApp()
  const [rows, setRows] = useState<DataModel[]>([])
  const [tree, setTree] = useState<TreeNode[]>([])
  const [folderModal, setFolderModal] = useState<{ mode: 'create' | 'rename'; node?: TreeNode; parentId?: string }>()
  const [folderName, setFolderName] = useState('')
  const [expandedKeys, setExpandedKeys] = useState<React.Key[]>(['__root__'])
  const [blankMenu, setBlankMenu] = useState<{ x: number; y: number } | null>(null)

  const fetchData = useCallback(async () => {
    const fetchAll = async (): Promise<DataModel[]> => {
      const out: DataModel[] = []
      const seen = new Set<string>()
      let page = 1
      let total = 0
      do {
        const r = await get<ListResp<DataModel>>(`/api/v1/models?project_id=${projectId}&page=${page}&page_size=500`)
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
    const [items, treeRes] = await Promise.all([
      fetchAll(),
      get<{ tree: TreeNode[] }>(`/api/v1/tree?project_id=${projectId}&kind=models`),
    ])
    return { items, tree: treeRes.tree }
  }, [projectId])

  const reloadSeqRef = useRef(0)
  const reload = useCallback(async () => {
    const seq = ++reloadSeqRef.current
    try {
      const data = await fetchData()
      if (seq !== reloadSeqRef.current) return
      setRows(data.items)
      setTree(data.tree)
    } catch (e: any) {
      if (seq !== reloadSeqRef.current) return
      message.error(e.message)
    }
  }, [fetchData])

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
    if (first || projectChanged || refreshChanged) void reload()
  }, [projectId, refresh, reload])

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

  const rowsById = useMemo(() => {
    const map = new Map<string, DataModel>()
    for (const m of rows) map.set(m.id, m)
    return map
  }, [rows])

  const remove = (m: DataModel) => {
    modal.confirm({
      title: `删除数据模型「${m.name}」？`,
      content: '删除后不可恢复，接口设计中对它的引用不会自动清理。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await del(`/api/v1/models/${m.id}`)
          message.success('已删除')
          onDeleted?.(m.id)
          void reload()
        } catch (e: any) {
          message.error(e.message)
        }
      },
    })
  }

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
          project_id: projectId, name: folderName.trim(), parent_id: folderModal.parentId || undefined,
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

  const removeFolder = (n: TreeNode) => {
    modal.confirm({
      title: `删除目录「${n.name}」？`,
      content: '目录及所有子目录会被删除；目录中的数据模型仅摘挂，模型本身不会被删除。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await del(`/api/v1/tree/folders/${n.id}`)
          message.success('已删除')
          void reload()
        } catch (e: any) {
          message.error(e.message)
        }
      },
    })
  }

  const unmountModel = (node: TreeNode) => {
    del(`/api/v1/tree/nodes/${node.id}`)
      .then(() => { message.success('已从目录移除'); void reload() })
      .catch((e: any) => message.error(e.message))
  }

  const folderMenu = (n: TreeNode): MenuProps => ({
    items: [
      { key: 'new-model', label: '新建结构', icon: <PlusOutlined /> },
      { key: 'new-folder', label: '新建子目录', icon: <FolderAddOutlined /> },
      { type: 'divider' },
      { key: 'rename', label: '重命名', icon: <EditOutlined /> },
      { type: 'divider' },
      { key: 'del', label: '删除目录', icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'new-model') {
        setExpandedKeys((prev) => (prev.includes(`folder-${n.id}`) ? prev : [...prev, `folder-${n.id}`]))
        onNewModel(n.id)
      } else if (key === 'new-folder') openFolderCreate(n.id)
      else if (key === 'rename') openFolderRename(n)
      else if (key === 'del') removeFolder(n)
    },
  })

  const modelMenu = (m: DataModel, node?: TreeNode): MenuProps => ({
    items: [
      ...(node ? [{ key: 'unmount', label: '从目录移除' }] : []),
      { type: 'divider' },
      { key: 'del', label: '删除结构', icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'unmount' && node) unmountModel(node)
      else if (key === 'del') remove(m)
    },
  })

  const openNewAtRoot = () => {
    setExpandedKeys((prev) => (prev.includes('__root__') ? prev : [...prev, '__root__']))
    onNewModel()
  }

  const rootMenuItems: MenuProps['items'] = [
    { key: 'new-model', label: '新建结构', icon: <PlusOutlined /> },
    { key: 'new-folder', label: '新建目录', icon: <FolderAddOutlined /> },
  ]
  const rootMenuClick = ({ key }: { key: string }) => {
    if (key === 'new-model') openNewAtRoot()
    else openFolderCreate()
  }

  // ---- 拖拽（与 ApiTreePanel 同一套语义：同父 reorder / 跨父 move / 未挂载 mount）----
  const parseKey = (k: string) =>
    k.startsWith('folder-') ? { kind: 'folder' as const, id: k.slice(7) }
      : k.startsWith('model-') ? { kind: 'model' as const, id: k.slice(6) }
        : { kind: 'root' as const, id: '' }

  const handleDrop = async (dragKey: string, dropKey: string, dropPos: number) => {
    const d = parseKey(dragKey)
    const t = parseKey(dropKey)
    const draggedNodeId = d.kind === 'folder' ? d.id : nodeMeta.byRef[d.id] ?? ''
    const insertIndex = (parentId: string, targetNodeId: string, after: boolean): number => {
      const ids = (nodeMeta.children[parentId] ?? []).map((s) => s.id).filter((id) => id !== draggedNodeId)
      const i = ids.indexOf(targetNodeId)
      return i < 0 ? ids.length : i + (after ? 1 : 0)
    }
    let parentId = ''
    let index: number | null = null
    if (t.kind === 'root') {
      parentId = ''
    } else if (t.kind === 'folder') {
      if (dropPos === 0) parentId = t.id
      else {
        parentId = nodeMeta.parent[t.id] ?? ''
        index = insertIndex(parentId, t.id, dropPos > 0)
      }
    } else {
      const nodeId = nodeMeta.byRef[t.id]
      if (nodeId) {
        parentId = nodeMeta.parent[nodeId] ?? ''
        index = insertIndex(parentId, nodeId, dropPos >= 0)
      }
    }
    try {
      if (!draggedNodeId) {
        await post('/api/v1/tree/nodes', {
          project_id: projectId, ref_type: 7, ref_id: d.id, parent_id: parentId || 0,
          index: index ?? undefined,
        })
      } else if ((nodeMeta.parent[draggedNodeId] ?? '') === parentId) {
        const siblings = nodeMeta.children[parentId] ?? []
        const ids = siblings.map((s) => s.id).filter((id) => id !== draggedNodeId)
        ids.splice(index ?? ids.length, 0, draggedNodeId)
        await put('/api/v1/tree/reorder', { parent_id: parentId || 0, ids })
      } else {
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
    const dropPos = info.dropPosition - Number(posArr[posArr.length - 1])
    void handleDrop(dragKey, dropKey, dropPos)
  }

  const toggleFolder = (key: string) => {
    setExpandedKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]))
  }

  const unmounted = rows.filter((m) => !mountedIds.has(m.id))

  const modelTitle = (m: DataModel, node?: TreeNode) => {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, paddingRight: 4 }}>
        <ApartmentOutlined style={{ color: '#eb2f96', fontSize: 13 }} />
        <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13, color: PALETTE.text }}>
          {m.name || '未命名结构'}
        </span>
        <Dropdown
          trigger={['click']}
          menu={{
            items: [
              ...(node ? [{ key: 'unmount', label: '从目录移除' }] : []),
              { key: 'del', label: '删除结构', danger: true },
            ],
            onClick: ({ key }) => {
              if (key === 'unmount' && node) unmountModel(node)
              else if (key === 'del') remove(m)
            },
          }}
        >
          <Button type="text" size="small" icon={<MoreOutlined />}
            style={{ color: PALETTE.textTertiary }}
            onClick={(e) => e.stopPropagation()} />
        </Dropdown>
      </div>
    )
  }

  const treeData = (() => {
    const walk = (nodes: TreeNode[]): any[] =>
      nodes.map((n) => {
        if (n.node_type === 1) {
          return {
            key: `folder-${n.id}`,
            title: (
              <Dropdown trigger={['contextMenu']} menu={folderMenu(n)}>
                <div
                  onDoubleClick={(e) => { e.stopPropagation(); toggleFolder(`folder-${n.id}`) }}
                  style={{ display: 'flex', alignItems: 'center', gap: 6, paddingRight: 4, borderRadius: 6 }}
                >
                  <FolderOutlined style={{ color: PALETTE.primary, fontSize: 14 }} />
                  <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>{n.name}</span>
                </div>
              </Dropdown>
            ),
            selectable: false,
            children: walk(n.children ?? []),
          }
        }
        const refId = n.ref_id ?? n.ref?.id ?? ''
        const m = rowsById.get(refId) ?? (n.ref ? { id: refId, name: n.ref.name } as DataModel : undefined)
        if (!m) return null
        return {
          key: `model-${m.id}`,
          title: (
            <Dropdown trigger={['contextMenu']} menu={modelMenu(m, n)}>
              {modelTitle(m, n)}
            </Dropdown>
          ),
        }
      }).filter(Boolean)
    const folderNodes = walk(tree)
    if (unmounted.length) {
      folderNodes.push(...unmounted.map((m) => ({
        key: `model-${m.id}`,
        title: (
          <Dropdown trigger={['contextMenu']} menu={modelMenu(m)}>
            {modelTitle(m)}
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
            onDoubleClick={(e) => { e.stopPropagation(); toggleFolder('__root__') }}
            style={{ display: 'flex', alignItems: 'center', gap: 6, borderRadius: 6 }}
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
      <div style={{ padding: '8px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Space size={6}>
            <span style={{ fontSize: 13, fontWeight: 600, color: PALETTE.text }}>结构</span>
            <span style={{ fontSize: 11, color: PALETTE.textTertiary }}>数据模型</span>
          </Space>
          <Space size={4}>
            <Button size="small" icon={<MenuFoldOutlined />}
              onClick={() => (expandedKeys.includes('__root__') ? setExpandedKeys([]) : setExpandedKeys(['__root__']))}
            />
            <Button size="small" icon={<FolderAddOutlined />} onClick={() => openFolderCreate()} />
            <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openNewAtRoot}>新建</Button>
          </Space>
        </div>
      </div>
      <div
        style={{ flex: 1, minHeight: 0, overflow: 'auto', padding: '4px 6px' }}
        onContextMenu={(e) => {
          if ((e.target as HTMLElement).closest('.ant-tree-treenode')) return
          e.preventDefault()
          setBlankMenu({ x: e.clientX, y: e.clientY })
        }}
      >
        <Tree
          showLine={{ showLeafIcon: false }}
          blockNode
          selectedKeys={activeId ? [`model-${activeId}`] : []}
          expandedKeys={expandedKeys}
          onExpand={(keys) => setExpandedKeys(keys)}
          treeData={treeData}
          draggable={{ icon: false, nodeDraggable: (node) => String(node.key) !== '__root__' }}
          onDrop={onTreeDrop}
          onSelect={(keys) => {
            const k = String(keys[0] ?? '')
            if (k.startsWith('model-')) onPick(k.slice(6))
          }}
        />
      </div>

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
    </div>
  )
}
