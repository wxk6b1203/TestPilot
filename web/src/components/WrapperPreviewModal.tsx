import { useState } from 'react'
import { Button, Modal, Space, Typography } from 'antd'
import { CopyOutlined, DownloadOutlined } from '@ant-design/icons'
import { get } from '../api'
import { message } from '../messageBridge'
import { t } from '../i18n'

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
      message.success(t('Copied to clipboard'))
    } catch {
      fallbackCopy(source)
      message.success(t('Copied to clipboard'))
    }
  }

  const downloadStub = async () => {
    setStubLoading(true)
    try {
      const sep = baseUrl.includes('?') ? '&' : '?'
      const r = await get<{ source: string }>(`${baseUrl}${sep}format=stub`)
      downloadText('tp_api_wrappers.pyi', r.source || '# (no APIs in this project yet)')
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
      title={title ?? t('tp_api_wrappers.py (auto-generated at dispatch time)')}
      footer={
        <Space>
          <Button icon={<CopyOutlined />} onClick={copy}>{t('Copy')}</Button>
          <Button icon={<DownloadOutlined />} onClick={() => downloadText('tp_api_wrappers.py', source)}>
            {t('Download .py')}
          </Button>
          <Button icon={<DownloadOutlined />} loading={stubLoading} onClick={downloadStub}>
            {t('Download .pyi stub')}
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
        {t('`.py` is the format the platform executes; `.pyi` is a self-contained completion stub — drop it into your local project and Pylance/Pyright resolves the {code1} types and the {code2} signature without installing testpilot-sdk.', { code1: 'Api<ID>', code2: 'run()' })}
      </Typography.Paragraph>
      <pre style={{
        fontFamily: MONO, fontSize: 12, maxHeight: 460, overflow: 'auto',
        background: '#0f172a', color: '#dbeafe', padding: 12, borderRadius: 6,
        whiteSpace: 'pre-wrap',
      }}>{source}</pre>
    </Modal>
  )
}
