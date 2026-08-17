import { useState } from 'react'
import { Button, Modal, Space, Typography } from 'antd'
import { CopyOutlined, DownloadOutlined } from '@ant-design/icons'
import { get } from '../api'
import { message } from '../messageBridge'

const MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

function fallbackCopy(text: string) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  document.execCommand('copy')
  document.body.removeChild(ta)
}

function downloadText(filename: string, text: string) {
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export default function WrapperPreviewModal({
  open, title, source, baseUrl, onClose,
}: {
  open: boolean
  title?: string
  source: string
  baseUrl: string
  onClose: () => void
}) {
  const [stubLoading, setStubLoading] = useState(false)

  const copy = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(source)
      } else {
        fallbackCopy(source)
      }
      message.success('已复制到剪贴板')
    } catch {
      fallbackCopy(source)
      message.success('已复制到剪贴板')
    }
  }

  const downloadStub = async () => {
    setStubLoading(true)
    try {
      const sep = baseUrl.includes('?') ? '&' : '?'
      const r = await get<{ source: string }>(`${baseUrl}${sep}format=stub`)
      downloadText('tp_api_wrappers.pyi', r.source || '# （项目内暂无接口）')
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setStubLoading(false)
    }
  }

  return (
    <Modal
      open={open}
      onCancel={onClose}
      width={760}
      title={title ?? 'tp_api_wrappers.py（派发时自动生成）'}
      footer={
        <Space>
          <Button icon={<CopyOutlined />} onClick={copy}>复制</Button>
          <Button icon={<DownloadOutlined />} onClick={() => downloadText('tp_api_wrappers.py', source)}>
            下载 .py
          </Button>
          <Button icon={<DownloadOutlined />} loading={stubLoading} onClick={downloadStub}>
            下载 .pyi 补全
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
        `.py` 为平台实际执行格式；`.pyi` 为自包含补全 stub——放到本地项目后
        Pylance/Pyright 可直接提示 <Typography.Text code>Api&lt;ID&gt;</Typography.Text> 的
        <Typography.Text code>run()</Typography.Text> 签名与响应字段，无需安装 testpilot-sdk。
      </Typography.Paragraph>
      <pre style={{
        fontFamily: MONO, fontSize: 12, maxHeight: 460, overflow: 'auto',
        background: '#0f172a', color: '#dbeafe', padding: 12, borderRadius: 6,
        whiteSpace: 'pre-wrap',
      }}>{source}</pre>
    </Modal>
  )
}
