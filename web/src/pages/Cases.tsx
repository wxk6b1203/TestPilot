import { Button, Card, Drawer, Form, Input, Modal, Popconfirm, Segmented, Space, Table, Tag, Typography, message } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, post } from '../api'
import type { ListResp, TestCase } from '../api'
import { useLayout } from './Layout'

const EXAMPLE = `{
  "steps": [
    {
      "id": "1", "type": 1, "name": "GET /json",
      "api_call": { "inline": { "method": 1, "uri": "/json" } }
    },
    {
      "id": "2", "type": 3, "name": "assert",
      "assertion": {
        "assertions": [
          { "target": 1, "op": 1, "expected": "200" },
          { "target": 4, "path": "$.user.name", "op": 1, "expected": "neo" }
        ]
      }
    },
    {
      "id": "3", "type": 4, "name": "extract",
      "set_var": { "key": "uid", "value_expr": "response.json.id" }
    }
  ]
}`

const LOWCODE_EXAMPLE = `from testpilot_sdk import Context, assert_that


async def run(ctx: Context):
    # HTTP 经能力桥由 Worker 执行；沙箱内无网络出口
    resp = await ctx.http("GET", "/json")
    assert_that(resp.status, "status").eq(200)
    assert_that(resp.body["user"]["name"], "user.name").eq("neo")
    await ctx.set_var("uid", resp.body["id"])
`

const STEP_TYPES = '1=API_CALL 2=GRPC_CALL 3=ASSERTION 4=SET_VAR 5=IF 6=LOOP 7=RETRY 9=DELAY'

export default function Cases() {
  const { projectId } = useLayout()
  const [rows, setRows] = useState<TestCase[]>([])
  const [open, setOpen] = useState(false)
  const [caseType, setCaseType] = useState<number>(1)
  const [viewing, setViewing] = useState<TestCase | null>(null)
  const [form] = Form.useForm()

  const load = () =>
    projectId
      ? get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=500`).then((r) => setRows(r.items))
      : Promise.resolve()
  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  return (
    <Card title="测试用例" extra={<Button type="primary" onClick={() => setOpen(true)}>新建用例</Button>}>
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={{ pageSize: 15 }}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '类型', dataIndex: 'type', width: 100, render: (v: number) => <Tag>{v === 1 ? '声明式' : '低代码'}</Tag> },
          { title: '描述', dataIndex: 'description' },
          {
            title: '操作',
            width: 160,
            render: (_, r) => (
              <Space>
                <Button size="small" onClick={() => setViewing(r)}>查看</Button>
                <Popconfirm title="删除用例？" onConfirm={async () => {
                  await del(`/api/v1/cases/${r.id}`)
                  load()
                }}>
                  <Button danger size="small">删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Modal
        title={caseType === 1 ? '新建声明式用例' : '新建低代码用例'}
        open={open}
        width={720}
        onCancel={() => setOpen(false)}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" onFinish={async (v) => {
          let definition: any
          if (caseType === 1) {
            try {
              definition = JSON.parse(v.definition)
            } catch (e: any) {
              message.error(`definition 不是合法 JSON：${e.message}`)
              return
            }
          } else {
            definition = { source: v.source, entry: v.entry || 'run' }
          }
          await post('/api/v1/cases', {
            project_id: projectId, name: v.name, description: v.description || '',
            type: caseType, definition,
          })
          setOpen(false)
          form.resetFields()
          load()
          message.success('已创建')
        }}>
          <Form.Item label="用例类型">
            <Segmented
              value={caseType}
              onChange={(v) => setCaseType(v as number)}
              options={[{ label: '声明式', value: 1 }, { label: '低代码（Python SDK）', value: 2 }]}
            />
          </Form.Item>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input />
          </Form.Item>
          {caseType === 1 ? (
            <>
              <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
                definition 为 DeclarativeCase 的 proto JSON。步骤类型：{STEP_TYPES}。
                断言 target：1=STATUS 2=HEADER 3=BODY 4=JSONPATH 5=ELAPSED；op：1=EQ 2=NE 3=EXISTS 5=CONTAINS 6=MATCHES 7=GT 8=LT 9=GE 10=LE 11=TYPE_IS。
              </Typography.Paragraph>
              <Form.Item name="definition" label="Definition（JSON）" initialValue={EXAMPLE} rules={[{ required: true }]}>
                <Input.TextArea rows={16} style={{ fontFamily: 'monospace', fontSize: 12 }} />
              </Form.Item>
            </>
          ) : (
            <>
              <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
                脚本在沙箱中运行：无网络出口（HTTP 经能力桥由 Worker 代执行）、环境变量白名单、CPU/内存受限。
                入口函数形如 <code>async def run(ctx: Context)</code>。
              </Typography.Paragraph>
              <Form.Item name="entry" label="入口函数" initialValue="run" rules={[{ required: true }]}>
                <Input style={{ width: 200 }} />
              </Form.Item>
              <Form.Item name="source" label="Source（Python）" initialValue={LOWCODE_EXAMPLE} rules={[{ required: true }]}>
                <Input.TextArea rows={16} style={{ fontFamily: 'monospace', fontSize: 12 }} />
              </Form.Item>
            </>
          )}
        </Form>
      </Modal>

      <Drawer title={viewing?.name} open={!!viewing} onClose={() => setViewing(null)} width={640}>
        {viewing?.type === 2 && viewing.definition?.source ? (
          <pre style={{ fontSize: 12 }}>{String(viewing.definition.source)}</pre>
        ) : (
          <pre style={{ fontSize: 12 }}>{viewing ? JSON.stringify(viewing.definition, null, 2) : ''}</pre>
        )}
      </Drawer>
    </Card>
  )
}
