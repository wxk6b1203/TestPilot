// 设计 token：参考 Apifox 的 IDE 视觉语言（浅色、细边框、语义方法色、4/8px 网格），
// 适配 TestPilot 自己的产品结构。全部页面统一从本文件取色。
import type { ThemeConfig } from 'antd'

export const PALETTE = {
  primary: '#4D6EEB',
  get: '#4CAF50',
  post: '#F56A2A',
  put: '#4D6EEB',
  delete: '#F54A45',
  patch: '#7C5CFC',
  casePurple: '#7C5CFC',
  success: '#52C41A',
  text: '#1F2329',
  textSecondary: '#646A73',
  textTertiary: '#BBBFC4',
  border: '#E5E7EB',
  bgLayout: '#F5F6F8',
  selectedRow: '#E9EBEF',
  topbar: '#EDEFF3',
} as const

export const SPACING = { 1: 4, 2: 8, 3: 12, 4: 16, 6: 24 } as const

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

export const themeConfig: ThemeConfig = {
  token: {
    colorPrimary: PALETTE.primary,
    colorInfo: PALETTE.primary,
    colorSuccess: PALETTE.success,
    colorText: PALETTE.text,
    colorTextSecondary: PALETTE.textSecondary,
    colorTextTertiary: PALETTE.textTertiary,
    colorBorder: PALETTE.border,
    colorBorderSecondary: PALETTE.border,
    colorBgLayout: PALETTE.bgLayout,
    colorBgContainer: '#FFFFFF',
    borderRadius: 6,
    controlHeight: 32,
    fontSize: 13,
  },
  components: {
    Menu: {
      itemSelectedBg: PALETTE.selectedRow,
      itemSelectedColor: PALETTE.text,
      itemHeight: 40,
    },
    Table: { rowSelectedBg: PALETTE.selectedRow, headerBg: '#FAFAFA' },
    Layout: { siderBg: '#FFFFFF', headerBg: PALETTE.topbar },
    Tabs: { inkBarColor: PALETTE.primary },
  },
}
