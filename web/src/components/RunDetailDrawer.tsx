import { Badge, Collapse, Descriptions, Drawer, Space, Tag, Typography } from 'antd'
import { useEffect, useState } from 'react'
import { getToken, STATUS } from '../api'
import type { Artifact, TestRun } from '../api'

export function StatusTag({ v }: { v: number }) {
  const s = STATUS[v] || { text: String(v), color: 'default' }
  return <Badge status={s.color as any} text={s.text} />
}

const ART_KIND: Record<number, string> = { 1: '截图', 2: '视频', 3: 'Trace', 4: 'HAR', 5: '下载', 6: '日志' }

function useArtifactUrl(id: string) {
  const [url, setUrl] = useState<string>()
  useEffect(() => {
    let obj: string | undefined
    let dead = false
    fetch(`/api/v1/artifacts/${id}/content`, { headers: { Authorization: `Bearer ${getToken()}` } })
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((b) => {
        if (dead) return
        obj = URL.createObjectURL(b)
        setUrl(obj)
      })
      .catch(() => undefined)
    return () => {
      dead = true
      if (obj) URL.revokeObjectURL(obj)
    }
  }, [id])
  return url
}

function ArtifactView({ a }: { a: Artifact }) {
  const url = useArtifactUrl(a.id)
  const name = a.uri.split('/').pop() || `artifact-${a.id}`
  if (!url) return <Tag>{ART_KIND[a.kind] || a.kind} 加载中…</Tag>
  if (a.kind === 1) {
    return (
      <a href={url} target="_blank" rel="noreferrer">
        <img src={url} alt={name} style={{ maxWidth: '100%', maxHeight: 320, border: '1px solid #444' }} />
      </a>
    )
  }
  const hint = a.kind === 3 ? '（可用 npx playwright show-trace 回放）' : ''
  return (
    <Typography.Link href={url} download={name}>
      {ART_KIND[a.kind] || '产物'}: {name}（{(a.size / 1024).toFixed(1)}KB）{hint}
    </Typography.Link>
  )
}

// 运行结果抽屉（运行记录页与用例编辑器共用）。
export default function RunDetailDrawer({
  run, open, onClose,
}: {
  run: TestRun | null
  open: boolean
  onClose: () => void
}) {
  return (
    <Drawer
      title={run ? `运行 ${run.id.slice(-8)} — ${STATUS[run.status]?.text ?? run.status}` : '运行结果'}
      open={open}
      onClose={onClose}
      width={860}
    >
      {run && (
        <>
          {run.summary?.error && (
            <Typography.Paragraph type="danger">{run.summary.error}</Typography.Paragraph>
          )}
          <Collapse
            defaultActiveKey={run.cases?.map((c) => c.id)}
            items={(run.cases || []).map((c) => ({
              key: c.id,
              label: (
                <Space wrap>
                  <StatusTag v={c.status} />
                  <b>{c.case_name || c.case_id}</b>
                  <Typography.Text type="secondary">{c.duration_ms}ms</Typography.Text>
                  {c.error && <Typography.Text type="danger">{c.error}</Typography.Text>}
                </Space>
              ),
              children: (
                <Collapse
                  ghost
                  items={c.steps.map((s) => ({
                    key: s.step_path,
                    label: (
                      <Space wrap>
                        <StatusTag v={s.status} />
                        <Typography.Text code>{s.step_path}</Typography.Text>
                        <Typography.Text type="secondary">{s.duration_ms}ms</Typography.Text>
                      </Space>
                    ),
                    children: (
                      <div style={{ fontSize: 12 }}>
                        {(s.logs || []).map((l, i) => (
                          <div key={i}><Typography.Text type="secondary">log: {l}</Typography.Text></div>
                        ))}
                        {(s.artifacts || []).map((a) => (
                          <div key={a.id} style={{ margin: '6px 0' }}><ArtifactView a={a} /></div>
                        ))}
                        {(s.assertions || []).map((a, i) => (
                          <div key={i}>
                            <Tag color={a.passed ? 'success' : 'error'}>{a.passed ? 'PASS' : 'FAIL'}</Tag>
                            <Typography.Text>
                              target={a.assertion?.target} path={a.assertion?.path || '-'} op={a.assertion?.op}，
                              实际 {a.actual || '-'}，期望 {a.assertion?.expected || '-'}（{a.message}）
                            </Typography.Text>
                          </div>
                        ))}
                        {s.request && (
                          <Descriptions size="small" column={1} style={{ marginTop: 8 }}
                            items={[
                              { key: 'q', label: '请求', children: <pre style={{ margin: 0 }}>{JSON.stringify(s.request, null, 2)}</pre> },
                              { key: 'p', label: '响应', children: <pre style={{ margin: 0, maxHeight: 240, overflow: 'auto' }}>{JSON.stringify(s.response, null, 2)}</pre> },
                            ]}
                          />
                        )}
                      </div>
                    ),
                  }))}
                />
              ),
            }))}
          />
        </>
      )}
    </Drawer>
  )
}
