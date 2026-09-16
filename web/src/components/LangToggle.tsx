import { Dropdown, Typography } from 'antd'
import { GlobalOutlined, CheckOutlined } from '@ant-design/icons'
import { LANGS, useI18n } from '../i18n'
import { PALETTE } from '../theme'

// 语言切换控件：顶栏与登录页共用；选择持久化在 localStorage('tp_lang')，
// 切换后 antd locale（ConfigProvider）与业务文案一起即时生效。
export default function LangToggle({ size = 13 }: { size?: number }) {
  const { lang, setLang } = useI18n()
  return (
    <Dropdown
      menu={{
        items: LANGS.map((l) => ({
          key: l.value,
          label: l.label,
          icon: l.value === lang
            ? <CheckOutlined />
            : <span style={{ display: 'inline-block', width: 14 }} />,
        })),
        onClick: ({ key }) => setLang(key as 'zh' | 'en'),
        selectedKeys: [lang],
      }}
    >
      <Typography.Link
        style={{ color: PALETTE.textSecondary, fontSize: size, whiteSpace: 'nowrap' }}
      >
        <GlobalOutlined /> {lang === 'zh' ? '中文' : 'EN'}
      </Typography.Link>
    </Dropdown>
  )
}
