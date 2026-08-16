// 设计 token：参考 Apifox 的 IDE 视觉语言（浅色、细边框、语义方法色、4/8px 网格），
// 适配 TestPilot 自己的产品结构。
// 全局主题统一走 antd ConfigProvider（见 main.tsx）；这里通过官方 getDesignToken
// 反解同一份 ThemeConfig，自定义组件使用的 PALETTE / SPACING 与 antd 组件保持一致。
import { theme } from 'antd'
import type { ThemeConfig } from 'antd'

const { defaultAlgorithm, getDesignToken } = theme

// 品牌种子色：antd 只负责派生，这里只保留色板输入
const SEED = {
  primary: '#4D6EEB',
  success: '#52C41A',
  text: '#1F2329',
  textSecondary: '#646A73',
  textTertiary: '#BBBFC4',
  border: '#E5E7EB',
  bgLayout: '#F5F6F8',
  container: '#FFFFFF',
} as const

// antd token 无法表达的产品语义色：HTTP 方法色、选中行、顶栏底色
const CUSTOM = {
  get: '#4CAF50',
  post: '#F56A2A',
  delete: '#F54A45',
  patch: '#7C5CFC',
  selectedRow: '#E9EBEF',
  topbar: '#EDEFF3',
} as const

// antd 官方全局主题配置：算法 + 全局 token + 组件 token，均在 ConfigProvider 中生效
export const themeConfig: ThemeConfig = {
  algorithm: defaultAlgorithm,
  token: {
    colorPrimary: SEED.primary,
    colorInfo: SEED.primary,
    colorSuccess: SEED.success,
    colorText: SEED.text,
    colorTextSecondary: SEED.textSecondary,
    colorTextTertiary: SEED.textTertiary,
    colorBorder: SEED.border,
    colorBorderSecondary: SEED.border,
    colorBgLayout: SEED.bgLayout,
    colorBgContainer: SEED.container,
    borderRadius: 6,
    controlHeight: 32,
    fontSize: 13,
  },
  components: {
    Menu: {
      itemSelectedBg: CUSTOM.selectedRow,
      itemSelectedColor: SEED.text,
      itemHeight: 40,
    },
    Table: { rowSelectedBg: CUSTOM.selectedRow, headerBg: '#FAFAFA' },
    Layout: { siderBg: SEED.container, headerBg: CUSTOM.topbar },
    Tabs: { inkBarColor: SEED.primary },
  },
}

// 官方静态消费方式：和 ConfigProvider 一样从 themeConfig 计算实际 token，
// 自定义组件的颜色与间距不再手写第二份值。
const tokens = getDesignToken(themeConfig)

export const PALETTE = {
  primary: tokens.colorPrimary,
  get: CUSTOM.get,
  post: CUSTOM.post,
  put: tokens.colorPrimary,
  delete: CUSTOM.delete,
  patch: CUSTOM.patch,
  casePurple: CUSTOM.patch,
  success: tokens.colorSuccess,
  text: tokens.colorText,
  textSecondary: tokens.colorTextSecondary,
  textTertiary: tokens.colorTextTertiary,
  border: tokens.colorBorder,
  bgLayout: tokens.colorBgLayout,
  selectedRow: CUSTOM.selectedRow,
  topbar: CUSTOM.topbar,
} as const

// 间距同样来自 antd 计算后的尺寸 token，保持 4/8/12/16/24 网格
export const SPACING = {
  1: tokens.paddingXXS,
  2: tokens.paddingXS,
  3: tokens.paddingSM,
  4: tokens.padding,
  6: tokens.marginLG,
} as const

// 方法语义色（标签/徽章/树节点共用）
export const METHOD_COLORS: Record<number, { text: string; color: string; bg: string }> = {
  1: { text: 'GET', color: PALETTE.get, bg: 'rgba(76,175,80,.10)' },
  2: { text: 'POST', color: PALETTE.post, bg: 'rgba(245,106,42,.10)' },
  3: { text: 'PUT', color: PALETTE.put, bg: 'rgba(77,110,235,.10)' },
  4: { text: 'DELETE', color: PALETTE.delete, bg: 'rgba(245,74,69,.10)' },
  5: { text: 'PATCH', color: PALETTE.patch, bg: 'rgba(124,92,252,.10)' },
  6: { text: 'HEAD', color: PALETTE.textSecondary, bg: 'rgba(100,106,115,.10)' },
  7: { text: 'OPTIONS', color: PALETTE.textSecondary, bg: 'rgba(100,106,115,.10)' },
}
