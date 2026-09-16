import { Button, Input, Space, Tabs, Upload } from 'antd'
import { FormatPainterOutlined, UploadOutlined } from '@ant-design/icons'
import { useRef, useState } from 'react'
import KvEditor from './KvEditor'
import type { Kv } from './KvEditor'
import { PALETTE } from '../theme'
import { t } from '../i18n'
import { message } from '../messageBridge'
import { uploadArtifact } from '../api'

// form 形状与 proto 对齐（FormData{fields:[…]}）：后端 protojson 直接解析，
// 旧数据里 form 为数组的形态在读取侧兼容（见 fieldsOf）。
export interface FormBody { fields: Kv[] }
export interface BodyValue { contentType: number; raw?: string; form?: FormBody; binary_ref?: string }

// 兼容读取：旧形状 form 是数组，新形状是 {fields:[…]}
const fieldsOf = (f: FormBody | Kv[] | undefined): Kv[] =>
  Array.isArray(f) ? f : f?.fields ?? []

// 请求体编辑器：无 / JSON / form-data / x-www-form-urlencoded。
// BodySpec 的 content 是 oneof（raw / form 互斥），写入时只保留当前 tab 的字段，
// 否则序列化出的 body 同时带 raw+form 会被 protojson 拒绝（后端静默丢弃）。
export default function BodyEditor({
  value, onChange,
}: {
  value: BodyValue
  onChange: (v: BodyValue) => void
}) {
  const raw = value.raw ?? ''
  const fields = fieldsOf(value.form)
  // 两类内容互斥存储，但 UI 侧各留草稿：切 tab 不清另一类，切回即恢复
  const rawDraftRef = useRef(raw)
  rawDraftRef.current = raw || rawDraftRef.current
  const formDraftRef = useRef(fields)
  formDraftRef.current = fields.length ? fields : formDraftRef.current
  const fmt = () => {
    try {
      onChange({ contentType: value.contentType, raw: JSON.stringify(JSON.parse(raw), null, 2) })
    } catch {
      message.error(t('Invalid JSON; cannot format'))
    }
  }
  const jsonTab = (
    <div>
      <Space style={{ marginBottom: 6 }}>
        <Button size="small" icon={<FormatPainterOutlined />} onClick={fmt}>{t('Format')}</Button>
      </Space>
      <Input.TextArea
        rows={10}
        value={raw}
        onChange={(e) => onChange({ contentType: value.contentType, raw: e.target.value })}
        style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }}
        placeholder={'{\n  "name": "neo"\n}'}
      />
    </div>
  )
  const binaryDraftRef = useRef(value.binary_ref ?? '')
  binaryDraftRef.current = value.binary_ref ?? binaryDraftRef.current
  const [uploading, setUploading] = useState(false)
  const binaryTab = (
    <div>
      <Space style={{ marginBottom: 6 }}>
        <Upload
          showUploadList={false}
          beforeUpload={(file) => {
            if (file.size <= 0 || file.size > 8 * 1024 * 1024) {
              message.error(t('File must be between 1B and 8MiB (binary_ref inline limit)'))
              return Upload.LIST_IGNORE
            }
            setUploading(true)
            uploadArtifact(file)
              .then((a) => {
                onChange({ contentType: 6, binary_ref: `artifact:${a.id}` })
                message.success(t('Uploaded → artifact:{id} (see it on the Artifacts page)', { id: a.id }))
              })
              .catch((e: Error) => message.error(e.message))
              .finally(() => setUploading(false))
            return false
          }}
        >
          <Button size="small" icon={<UploadOutlined />} loading={uploading}>
            {t('Upload file to create a reference')}
          </Button>
        </Upload>
        {value.binary_ref?.startsWith('artifact:') && (
          <span style={{ fontSize: 12, color: PALETTE.textTertiary }}>{value.binary_ref}</span>
        )}
      </Space>
      <Input.TextArea
        rows={6}
        value={value.binary_ref ?? binaryDraftRef.current}
        onChange={(e) => onChange({ contentType: 6, binary_ref: e.target.value })}
        placeholder={t('artifact:<artifact ID> or base64:<base64 data>')}
        style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }}
      />
      <div style={{ fontSize: 12, color: '#8c8c8c', marginTop: 6 }}>
        {t('binary_ref accepts artifact:<id> (the artifact is read by the Scheduler at dispatch, ≤8MiB) and base64:<payload> (inlined); you can also browse/upload on the Artifacts page')}
      </div>
    </div>
  )
  const formTab = (ct: number) => (
    <KvEditor
      value={fields}
      onChange={(kv) => onChange({ contentType: ct, form: { fields: kv } })}
      keyPlaceholder={t('Field name')} valuePlaceholder={t('Field value')}
    />
  )
  const tab = (() => {
    switch (value.contentType) {
      case 4: return 'json'
      case 2: return 'form-data'
      case 3: return 'urlencoded'
      case 6: return 'binary'
      default: return 'none'
    }
  })()
  return (
    <Tabs
      size="small"
      activeKey={tab}
      onChange={(k) => {
        if (k === 'none') onChange({ contentType: 0 })
        else if (k === 'json') onChange({ contentType: 4, raw: rawDraftRef.current })
        else if (k === 'form-data') onChange({ contentType: 2, form: { fields: formDraftRef.current } })
        else if (k === 'binary') onChange({ contentType: 6, binary_ref: binaryDraftRef.current })
        else onChange({ contentType: 3, form: { fields: formDraftRef.current } })
      }}
      items={[
        { key: 'none', label: t('None'), children: <span style={{ color: PALETTE.textTertiary }}>{t('No body')}</span> },
        { key: 'json', label: 'JSON', children: jsonTab },
        { key: 'form-data', label: 'form-data', children: formTab(2) },
        { key: 'urlencoded', label: 'x-www-form-urlencoded', children: formTab(3) },
        { key: 'binary', label: t('Binary reference'), children: binaryTab },
      ]}
    />
  )
}
