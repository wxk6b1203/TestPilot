import { Badge, Button, Collapse, Descriptions, Drawer, Space, Tag, Typography } from 'antd'
import { DownloadOutlined } from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { download, getToken, STATUS } from '../api'
import type { Artifact, TestRun } from '../api'
import { message } from '../messageBridge'
import { t } from '../i18n'

export function StatusTag({ v }: { v: number }) {
  const s = STATUS[v] || { text: String(v), color: 'default' }
  return <Badge status={s.color as any} text={s.text} />
}

// getter 每次读取求值：语言切换即时生效
const ART_KIND: Record<number, string> = {
  get 1() { return t('Screenshot') }, get 2() { return t('Video') }, get 3() { return 'Trace' },
  get 4() { return 'HAR' }, get 5() { return t('Download') }, get 6() { return t('Log') },
}

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
  if (!url) return <Tag>{ART_KIND[a.kind] || a.kind} {t('loading…')}</Tag>
  if (a.kind === 1) {
    return (
      <a href={url} target="_blank" rel="noreferrer">
        <img src={url} alt={name} style={{ maxWidth: '100%', maxHeight: 320, border: '1px solid #444' }} />
      </a>
    )
  }
  const hint = a.kind === 3 ? t('(replay with npx playwright show-trace)') : ''
  return (
    <Typography.Link href={url} download={name}>
      {ART_KIND[a.kind] || t('Artifact')}: {name} ({(a.size / 1024).toFixed(1)}KB){hint}
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
      title={run ? t('Run {id} — {status}', { id: run.id.slice(-8), status: STATUS[run.status]?.text ?? String(run.status) }) : t('Run result')}
      open={open}
      onClose={onClose}
      width={860}
    >
      {run && (
        <>
          <div style={{ marginBottom: 12 }}>
            <Button
              size="small"
              icon={<DownloadOutlined />}
              onClick={() => {
                download(`/api/v1/runs/${run.id}/junit`, `testpilot-run-${run.id}.xml`)
                  .catch((e) => message.error(e.message))
              }}
            >
              {t('Export JUnit')}
            </Button>
          </div>
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
                              {t('actual {actual}, expected {expected} ({message})', { actual: a.actual || '-', expected: a.assertion?.expected || '-', message: a.message })}
                            </Typography.Text>
                          </div>
                        ))}
                        {s.request && (
                          <Descriptions size="small" column={1} style={{ marginTop: 8 }}
                            items={[
                              { key: 'q', label: t('Request'), children: <pre style={{ margin: 0 }}>{JSON.stringify(s.request, null, 2)}</pre> },
                              { key: 'p', label: t('Response'), children: <pre style={{ margin: 0, maxHeight: 240, overflow: 'auto' }}>{JSON.stringify(s.response, null, 2)}</pre> },
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
