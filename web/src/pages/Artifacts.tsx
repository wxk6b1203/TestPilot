// 产物管理：全租户产物清单（运行产物 + 用户上传），binary_ref 的引用源在这里。
import { useEffect, useState } from 'react'
import { Button, Select, Space, Table, Tag, Typography, Upload } from 'antd'
import { DownloadOutlined, UploadOutlined } from '@ant-design/icons'
import {
  ARTIFACT_KINDS, download, listArtifacts, uploadArtifact,
} from '../api'
import type { Artifact, ListResp } from '../api'
import { message } from '../messageBridge'

function fmtSize(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`
  return `${n} B`
}

const fileName = (uri: string) => uri.split('/').pop() ?? uri

export default function Artifacts() {
  const [items, setItems] = useState<Artifact[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [kind, setKind] = useState<number | undefined>()
  const [uploading, setUploading] = useState(false)
  const [pageSize, setPageSize] = useState(10)

  const load = () =>
    listArtifacts({ kind, page, page_size: pageSize })
      .then((r: ListResp<Artifact>) => {
        setItems(r.items ?? [])
        setTotal(r.total ?? 0)
      })
      .catch((e: Error) => message.error(e.message))

  useEffect(() => {
    load().catch(() => {})
    // load 随 page/pageSize/kind 变化重新拉取（函数内闭包取最新 state）
    // oxlint 不强制 exhaustive-deps；此处不把 load 入列避免无谓重拉
  }, [page, pageSize, kind])

  return (
    <div style={{ padding: 16 }}>
      <Space style={{ marginBottom: 12 }}>
        <Select
          allowClear placeholder="全部类型" style={{ width: 140 }} value={kind}
          onChange={(v) => { setKind(v); setPage(1) }}
          options={Object.entries(ARTIFACT_KINDS).map(([v, label]) => ({ value: Number(v), label }))}
        />
        <Upload
          showUploadList={false}
          beforeUpload={(file) => {
            if (file.size <= 0 || file.size > 8 * 1024 * 1024) {
              message.error('文件需在 1B..8MiB 之间（binary_ref 内联上限）')
              return Upload.LIST_IGNORE
            }
            setUploading(true)
            uploadArtifact(file)
              .then(() => { message.success('已上传'); load() })
              .catch((e: Error) => message.error(e.message))
              .finally(() => setUploading(false))
            return false
          }}
        >
          <Button type="primary" icon={<UploadOutlined />} loading={uploading}>
            上传产物
          </Button>
        </Upload>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          上传的二进制可在接口 body 的 binary_ref 中以 artifact:&lt;id&gt; 引用（≤8MiB）
        </Typography.Text>
      </Space>
      <Table<Artifact>
        size="small" rowKey="id" dataSource={items}
        pagination={{
          current: page, pageSize, total, showSizeChanger: true,
          pageSizeOptions: [10, 20, 50, 100],
          onChange: (p, ps) => { setPage(p); setPageSize(ps) },
        }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 200, render: (v: string) => <Typography.Text copyable style={{ fontSize: 12 }}>{v}</Typography.Text> },
          { title: '类型', dataIndex: 'kind', width: 80, render: (v: number) => <Tag>{ARTIFACT_KINDS[v] ?? v}</Tag> },
          { title: '文件名', dataIndex: 'uri', render: (v: string) => fileName(v) },
          { title: '大小', dataIndex: 'size', width: 90, render: fmtSize },
          {
            title: '来源', dataIndex: 'run_id', width: 110,
            render: (v: string) => (v && v !== '0' ? <Tag>运行 {v.slice(-6)}</Tag> : <Tag>上传</Tag>),
          },
          {
            title: '时间', dataIndex: 'created_at', width: 160,
            render: (v: number) => (v ? new Date(v).toLocaleString() : '—'),
          },
          {
            title: '操作', width: 90,
            render: (_, row) => (
              <Button size="small" icon={<DownloadOutlined />}
                onClick={() => download(`/api/v1/artifacts/${row.id}/content`, fileName(row.uri))} />
            ),
          },
        ]}
      />
    </div>
  )
}
