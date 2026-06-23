export interface ThemeTokens {
  name: string
  // Base
  appBg: string
  headerBg: string
  headerBorder: string
  sidebarBg: string
  panelBg: string
  panelBorder: string
  // Text
  textPrimary: string
  textMuted: string
  textDim: string
  // Accent
  accent: string
  accentDim: string
  // Semantic
  positive: string
  negative: string
  // Fonts
  fontSerif: string
  fontMono: string
  fontUi: string
  // Agent role colours (Farmer, Merchant, Guard, Artisan)
  roleColours: readonly [string, string, string, string]
  // Canvas terrain
  canvasBg: string
  gridColor: string
  riverColor: string
  forestColor: string
  fieldColor: string
  roadColor: string
  buildingColor: string
  // Glowing effects (dark theme only)
  glow: boolean
}

export const LIGHT: ThemeTokens = {
  name: 'Cartograph',
  appBg: '#ddd9d0',
  headerBg: '#3a2010',
  headerBorder: '#c49a28',
  sidebarBg: '#e8d5a3',
  panelBg: '#d4c090',
  panelBorder: '#c0a876',
  textPrimary: '#2c1810',
  textMuted: '#7a4e2d',
  textDim: '#a08050',
  accent: '#c49a28',
  accentDim: '#5a3010',
  positive: '#5a8040',
  negative: '#c04040',
  fontSerif: "'Cinzel', serif",
  fontMono: "'Space Mono', monospace",
  fontUi: "'Lora', Georgia, serif",
  roleColours: ['#c03838', '#3858c0', '#408040', '#9040a0'],
  canvasBg: '#dfc898',
  gridColor: 'rgba(180,140,60,0.18)',
  riverColor: '#7aace080',
  forestColor: 'rgba(70,110,42,0.55)',
  fieldColor: '#b8a040',
  roadColor: '#c8a868',
  buildingColor: '#7a5030',
  glow: false,
}

export const DARK: ThemeTokens = {
  name: 'Dungeon HUD',
  appBg: '#0e0c0a',
  headerBg: '#1e1a16',
  headerBorder: '#2a2018',
  sidebarBg: '#1a1610',
  panelBg: '#201c18',
  panelBorder: '#2a2018',
  textPrimary: '#e8dcc8',
  textMuted: '#a07840',
  textDim: '#6a5838',
  accent: '#d4a853',
  accentDim: '#201c18',
  positive: '#6aaa58',
  negative: '#c05040',
  fontSerif: "'Cinzel', serif",
  fontMono: "'Space Mono', monospace",
  fontUi: "'Space Mono', monospace",
  roleColours: ['#ff5030', '#4080ff', '#40d060', '#c040d0'],
  canvasBg: '#090d0b',
  gridColor: '#131c14',
  riverColor: '#1a40a8',
  forestColor: '#0c1a0d',
  fieldColor: '#131e0f',
  roadColor: '#2a2210',
  buildingColor: '#2a2018',
  glow: true,
}
