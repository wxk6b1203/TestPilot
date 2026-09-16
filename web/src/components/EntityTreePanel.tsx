import {
  App as AntdApp,
  Button,
  Dropdown,
  Input,
  Modal,
  Select,
  Space,
  Tooltip,
  Tree,
} from 'antd'
import type { MenuProps, TreeProps } from 'antd'
import {
  ClusterOutlined, DeleteOutlined, EditOutlined, FileTextOutlined, FolderAddOutlined,
  FolderOpenOutlined, FolderOutlined, MenuFoldOutlined, MoreOutlined, PlusOutlined,
} from '@ant-design/icons'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { del, get, post, put } from '../api'
import type { ListResp, TreeNode } from '../api'
import { PALETTE } from '../theme'
import { message } from '../messageBridge'
import { t } from '../i18n'

// 通用实体目录树面板（用例 / 套件）：
// - 目录结构与接口树共用 tree_nodes，按 kind=case|suite 过滤叶子
// - 支持目录 CRUD、右键菜单、移动到目录、摘挂、删除实体
// - 搜索时把未挂载/命中实体平铺到根目录尾部

interface Props {
  title: string
  kind: 'case' | 'suite'
  projectId: string
  activeId?: string
  refresh: number
  onPick: (id: string) => void
  onNewInFolder?: (parentId?: string) => void
  onDeleted?: (id: string) => void
}

interface EntityRow {
  id: string
  name: string
  description?: string
  type?: number
}

export default function EntityTreePanel({
  title, kind, projectId, activeId, refresh, onPick, onNewInFolder, onDeleted,
}: Props) {
  const { modal } = AntdApp.useApp()
  const [rows, setRows] = useState<EntityRow[]>([])
  const [tree, setTree] = useState<TreeNode[]>([])
  const [search, setSearch] = useState('')
  const [folderModal, setFolderModal] = useState<{ mode: 'create' | 'rename'; node?: TreeNode; parentId?: string }>()
  const [folderName, setFolderName] = useState('')
  const [moveItem, setMoveItem] = useState<EntityRow | null>(null)
  const [moveTarget, setMoveTarget] = useState('')
  const [expandedKeys, setExpandedKeys] = useState<React.Key[]>(['__root__'])

  const fetchData = useCallback(async () => {
    const listPath = kind === 'case'
      ? `/api/v1/cases?project_id=${projectId}&page_size=500`
      : `/api/v1/suites?project_id=${projectId}&page_size=500`
    const [items, treeRes] = await Promise.all([
      get<ListResp<EntityRow>>(listPath),
      get<{ tree: TreeNode[] }>(`/api/v1/tree?project_id=${projectId}&kind=${kind}`),
    ])
    return { items: items.items, tree: treeRes.tree }
  }, [projectId, kind])

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
    if (first || projectChanged || refreshChanged) {
      void reload()
    }
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
    const map = new Map<string, EntityRow>()
    for (const a of rows) map.set(a.id, a)
    return map
  }, [rows])

  const filtered = useMemo(() => {
    const kw = search.trim().toLowerCase()
    return kw
      ? rows.filter((a) => (a.name || '').toLowerCase().includes(kw) || (a.description ?? '').toLowerCase().includes(kw))
      : rows
  }, [rows, search])

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
      message.warning(t('Enter a folder name'))
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
      message.success(t('Saved'))
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
      message.success(t('Folder and subfolders deleted (entities inside are only unmounted)'))
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const confirmRemoveFolder = (n: TreeNode) => {
    modal.confirm({
      title: t('Delete folder "{name}"?', { name: n.name }),
      content: t('The folder and all subfolders will be deleted; cases/suites inside are only unmounted and will not be deleted.'),
      okText: t('Delete'),
      okButtonProps: { danger: true },
      cancelText: t('Cancel'),
      onOk: () => removeFolder(n),
    })
  }

  const unmountNode = (node: TreeNode) => {
    del(`/api/v1/tree/nodes/${node.id}`)
      .then(() => { message.success(t('Removed from folder')); void reload() })
      .catch((e: any) => message.error(e.message))
  }

  const openMove = (item: EntityRow) => {
    setMoveTarget('')
    setMoveItem(item)
  }

  const submitMove = async () => {
    if (!moveItem) return
    if (!moveTarget) {
      message.warning(t('Select a target folder'))
      return
    }
    const nodeId = nodeMeta.byRef[moveItem.id]
    try {
      if (moveTarget === '__unmount__') {
        if (!nodeId) {
          message.info(t('This item is already uncategorized'))
          setMoveTarget('')
          setMoveItem(null)
          return
        }
        await del(`/api/v1/tree/nodes/${nodeId}`)
      } else {
        const targetParentId = moveTarget === '__root__' ? '' : moveTarget
        if (nodeId && (nodeMeta.parent[nodeId] ?? '') === targetParentId) {
          message.info(t('This item is already in this folder'))
          setMoveTarget('')
          setMoveItem(null)
          return
        }
        const parentId = moveTarget === '__root__' ? 0 : moveTarget
        const refType = kind === 'case' ? 4 : 5
        if (nodeId) {
          await put(`/api/v1/tree/nodes/${nodeId}/move`, { parent_id: parentId })
        } else {
          await post('/api/v1/tree/nodes', {
            project_id: projectId, ref_type: refType, ref_id: moveItem.id, parent_id: parentId,
          })
        }
      }
      message.success(t('Moved'))
      setMoveTarget('')
      setMoveItem(null)
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const removeEntity = async (item: EntityRow) => {
    const path = kind === 'case' ? `/api/v1/cases/${item.id}` : `/api/v1/suites/${item.id}`
    try {
      await del(path)
      message.success(t('Deleted'))
      onDeleted?.(item.id)
      void reload()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const confirmRemoveEntity = (item: EntityRow) => {
    const noun = kind === 'case' ? t('case') : t('suite')
    modal.confirm({
      title: t('Delete {noun} "{name}"?', { noun, name: item.name }),
      content: t('This cannot be undone; its tree mounts will be removed as well.'),
      okText: t('Delete'),
      okButtonProps: { danger: true },
      cancelText: t('Cancel'),
      onOk: () => removeEntity(item),
    })
  }

  const folderMenu = (n: TreeNode): MenuProps => ({
    items: [
      ...(onNewInFolder ? [{ key: 'new-entity', label: kind === 'case' ? t('New case') : t('New suite'), icon: <PlusOutlined /> }] : []),
      { key: 'new-folder', label: t('New subfolder'), icon: <FolderAddOutlined /> },
      { type: 'divider' },
      { key: 'rename', label: t('Rename'), icon: <EditOutlined /> },
      { type: 'divider' },
      { key: 'del', label: t('Delete folder'), icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'new-entity') {
        setExpandedKeys((prev) => (prev.includes(`folder-${n.id}`) ? prev : [...prev, `folder-${n.id}`]))
        onNewInFolder?.(n.id)
      } else if (key === 'new-folder') openFolderCreate(n.id)
      else if (key === 'rename') openFolderRename(n)
      else if (key === 'del') confirmRemoveFolder(n)
    },
  })

  const entityMenu = (item: EntityRow, node?: TreeNode): MenuProps => ({
    items: [
      { key: 'open', label: t('Open') },
      { key: 'move', label: t('Move to folder…') },
      ...(node ? [{ key: 'unmount', label: t('Remove from folder') }] : []),
      { type: 'divider' },
      { key: 'del', label: kind === 'case' ? t('Delete case') : t('Delete suite'), icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'open') onPick(item.id)
      else if (key === 'move') openMove(item)
      else if (key === 'unmount' && node) unmountNode(node)
      else if (key === 'del') confirmRemoveEntity(item)
    },
  })

  const rootMenuItems: MenuProps['items'] = [
    ...(onNewInFolder ? [{ key: 'new-entity', label: kind === 'case' ? t('New case') : t('New suite'), icon: <PlusOutlined /> }] : []),
    { key: 'new-folder', label: t('New folder'), icon: <FolderAddOutlined /> },
  ]
  const rootMenuClick = ({ key }: { key: string }) => {
    if (key === 'new-entity') onNewInFolder?.()
    else openFolderCreate()
  }

  const folderOptions = useMemo(() => {
    const moveNodeId = moveItem ? nodeMeta.byRef[moveItem.id] : undefined
    const currentParentId = moveNodeId ? nodeMeta.parent[moveNodeId] ?? '' : null
    const isCurrent = (value: string) => currentParentId !== null && (value === '__root__' ? '' : value) === currentParentId
    const out: { value: string; label: string; disabled?: boolean }[] = [
      { value: '__root__', label: t('(Root)'), disabled: isCurrent('__root__') },
      { value: '__unmount__', label: t('(Uncategorized)'), disabled: !!moveItem && !moveNodeId },
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
  }, [tree, moveItem, nodeMeta])

  const toggleFolder = (key: string) => {
    setExpandedKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]))
  }

  // ---- 拖拽：与接口树一致，支持目录/实体同父排序、跨目录移动、未挂载挂载 ----
  const parseKey = (k: string) =>
    k.startsWith('folder-') ? { kind: 'folder' as const, id: k.slice(7) }
    : k.startsWith('entity-') ? { kind: 'entity' as const, id: k.slice(7) }
    : { kind: 'root' as const, id: '' }

  const handleDrop = async (dragKey: string, dropKey: string, dropPos: number) => {
    const d = parseKey(dragKey)
    const tgt = parseKey(dropKey)
    // TODO(拖拽隐患): 插入点解析依赖 nodeMeta.byRef / nodeMeta.parent 的时效性。
    // 若上一次拖拽后 reload 尚未完成就连续拖拽，第二次可能查不到 byRef 而
    // 被误判为「未挂载」走 mount 分支，产生重复树节点。后续应加防护：
    // dragKey 校验失败时拒绝静默挂载（提示刷新），或改为乐观更新单数据源。
    const draggedNodeId = d.kind === 'folder' ? d.id : nodeMeta.byRef[d.id] ?? ''

    // 插入点统一按「排除被拖节点后的兄弟序」计算：若用包含被拖节点的数组
    // findIndex，同父向下拖时目标序号会偏大 1，splice 落点后移一位
    const insertIndex = (parentId: string, targetNodeId: string, after: boolean): number => {
      const ids = (nodeMeta.children[parentId] ?? [])
        .map((s) => s.id).filter((id) => id !== draggedNodeId)
      const i = ids.indexOf(targetNodeId)
      return i < 0 ? ids.length : i + (after ? 1 : 0)
    }

    let parentId = ''
    let index: number | null = null
    if (tgt.kind === 'root') {
      parentId = ''
    } else if (tgt.kind === 'folder') {
      if (dropPos === 0) {
        parentId = tgt.id
      } else {
        parentId = nodeMeta.parent[tgt.id] ?? ''
        index = insertIndex(parentId, tgt.id, dropPos > 0)
      }
    } else {
      const nodeId = nodeMeta.byRef[tgt.id]
      if (nodeId) {
        parentId = nodeMeta.parent[nodeId] ?? ''
        index = insertIndex(parentId, nodeId, dropPos >= 0)
      }
    }
    try {
      if (!draggedNodeId) {
        await post('/api/v1/tree/nodes', {
          project_id: projectId,
          ref_type: kind === 'case' ? 4 : 5,
          ref_id: d.id,
          parent_id: parentId || 0,
          index: index ?? undefined,
        })
      } else if ((nodeMeta.parent[draggedNodeId] ?? '') === parentId) {
        const siblings = nodeMeta.children[parentId] ?? []
        const ids = siblings.map((s) => s.id).filter((id) => id !== draggedNodeId)
        const at = index ?? ids.length
        ids.splice(at, 0, draggedNodeId)
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


  const leafTitle = (item: EntityRow, node?: TreeNode) => {
    const Icon = kind === 'case' ? FileTextOutlined : ClusterOutlined
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, paddingRight: 4 }}>
        <Icon style={{ color: PALETTE.primary, fontSize: 14, flexShrink: 0 }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            fontSize: 13, color: PALETTE.text,
          }}>
            {item.name || t('Untitled')}
          </div>
          {item.description && (
            <div style={{ fontSize: 11, color: PALETTE.textTertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {item.description}
            </div>
          )}
        </div>
        <Dropdown trigger={['click']} menu={entityMenu(item, node)}>
          <Button type="text" size="small" icon={<MoreOutlined />}
            style={{ color: PALETTE.textTertiary }}
            onClick={(e) => e.stopPropagation()} />
        </Dropdown>
      </div>
    )
  }

  const unmounted = useMemo(
    () => filtered.filter((a) => !mountedIds.has(a.id)),
    [filtered, mountedIds],
  )

  // 搜索语义：实体按名称/描述命中保留；目录因子孙命中而保留（无命中的目录剪掉）；
  // 未挂载命中实体平铺到根尾。原先 treeData 只用全量 tree 构建、未挂载区又仅在非搜索时
  // 追加，导致面板搜索完全无效。
  const searching = !!search.trim()
  const hitFolderKeys: string[] = []
  const treeData = (() => {
    const kw = search.trim().toLowerCase()
    const hit = (a?: EntityRow | null) =>
      !kw || (!!a && ((a.name || '').toLowerCase().includes(kw) || (a.description ?? '').toLowerCase().includes(kw)))
    const walk = (nodes: TreeNode[]): any[] =>
      nodes.map((n) => {
        if (n.node_type === 1) {
          const kids = walk(n.children ?? [])
          if (kw && !kids.length) return null
          if (kw) hitFolderKeys.push(`folder-${n.id}`)
          return {
            key: `folder-${n.id}`,
            title: (
              <Dropdown trigger={['contextMenu']} menu={folderMenu(n)}>
                <div
                  onDoubleClick={(e) => { e.stopPropagation(); toggleFolder(`folder-${n.id}`) }}
                  style={{ display: 'flex', alignItems: 'center', gap: 6, paddingRight: 4, borderRadius: 6 }}
                >
                  <FolderOutlined style={{ color: PALETTE.primary, fontSize: 14 }} />
                  <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
                    {n.name}
                  </span>
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: [
                        { key: 'sub', label: t('New subfolder'), icon: <FolderAddOutlined /> },
                        { key: 'rename', label: t('Rename'), icon: <EditOutlined /> },
                        { key: 'del', label: t('Delete folder'), danger: true },
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
            children: kids,
          }
        }
        const refId = n.ref_id ?? (n.ref as any)?.id ?? ''
        const item = rowsById.get(refId) ?? (n.ref ? {
          id: refId, name: (n.ref as any).name, description: (n.ref as any).description,
          type: (n.ref as any).type,
        } as EntityRow : undefined)
        if (!item || !hit(item)) return null
        return {
          key: `entity-${item.id}`,
          title: (
            <Dropdown trigger={['contextMenu']} menu={entityMenu(item, n)}>
              {leafTitle(item, n)}
            </Dropdown>
          ),
        }
      }).filter(Boolean)
    const children = walk(tree)
    // 未挂载实体：非搜索时平铺根尾；搜索时同样平铺命中项（原先搜索时整段被藏掉）
    if (unmounted.length) {
      children.push(...unmounted.map((a) => ({
        key: `entity-${a.id}`,
        title: (
          <Dropdown trigger={['contextMenu']} menu={entityMenu(a)}>
            {leafTitle(a)}
          </Dropdown>
        ),
      })))
    }
    return [{
      key: '__root__',
      title: (
        <Dropdown trigger={['contextMenu']} menu={{ items: rootMenuItems, onClick: rootMenuClick }}>
          <div
            onDoubleClick={(e) => { e.stopPropagation(); toggleFolder('__root__') }}
            style={{ display: 'flex', alignItems: 'center', gap: 6, borderRadius: 6 }}
          >
            <FolderOpenOutlined style={{ color: PALETTE.primary, fontSize: 14 }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: PALETTE.text }}>{title}</span>
          </div>
        </Dropdown>
      ),
      selectable: false,
      children,
    }]
  })()

  const onSelect: TreeProps['onSelect'] = (keys) => {
    const k = String(keys[0] ?? '')
    if (k.startsWith('entity-')) onPick(k.slice(7))
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ padding: '10px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>{title}</span>
          <Space size={4}>
            <Button size="small" icon={<MenuFoldOutlined />}
              onClick={() => (expandedKeys.includes('__root__') ? setExpandedKeys([]) : setExpandedKeys(['__root__']))}
            />
            <Tooltip title={t('New folder')}>
              <Button size="small" icon={<FolderAddOutlined />} onClick={() => openFolderCreate()} />
            </Tooltip>
            {onNewInFolder && (
              <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => onNewInFolder?.()}>
                {t('New')}
              </Button>
            )}
          </Space>
        </div>
        <Input
          size="small" allowClear value={search} onChange={(e) => setSearch(e.target.value)}
          placeholder={t('Search name/description')}
        />
      </div>
      <div style={{ flex: 1, overflow: 'auto', padding: '4px 6px' }}>
        <Tree
          showLine={{ showLeafIcon: false }}
          blockNode
          // 搜索视图是过滤后的局部树：命中目录全部展开让结果直接可见；拖拽排序基准
          // 不再是全量兄弟序，禁用（与接口树「搜索走列表视图不可拖」一致）
          draggable={searching ? false : { icon: false, nodeDraggable: (node) => String(node.key) !== '__root__' }}
          selectedKeys={activeId ? [`entity-${activeId}`] : []}
          expandedKeys={searching ? Array.from(new Set([...expandedKeys, ...hitFolderKeys])) : expandedKeys}
          onExpand={(keys) => setExpandedKeys(keys)}
          treeData={treeData}
          onDrop={onTreeDrop}
          onSelect={onSelect}
        />
      </div>

      <Modal
        title={folderModal?.mode === 'rename' ? t('Rename folder') : t('New folder')}
        open={!!folderModal}
        okText={t('Save')}
        onCancel={() => setFolderModal(undefined)}
        onOk={submitFolder}
        destroyOnHidden
      >
        <Input
          value={folderName}
          onChange={(e) => setFolderName(e.target.value)}
          onPressEnter={submitFolder}
          placeholder={t('Folder name')}
        />
      </Modal>

      <Modal
        title={t('Move {noun}', { noun: kind === 'case' ? t('case') : t('suite') })}
        open={!!moveItem}
        okText={t('Move')}
        onCancel={() => setMoveItem(null)}
        onOk={submitMove}
        destroyOnHidden
      >
        <Select
          style={{ width: '100%' }}
          value={moveTarget || undefined}
          placeholder={t('Select target folder')}
          onChange={setMoveTarget}
          options={folderOptions}
        />
      </Modal>
    </div>
  )
}
