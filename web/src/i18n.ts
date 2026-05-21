import type { Language } from './types';

export const LANGUAGE_STORAGE_KEY = 'serial-platform.language';

export const languages: { value: Language; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'zh-CN', label: '中文' }
];

export const messages = {
  en: {
    appName: 'Serial Platform',
    centralServer: 'central-server',
    navAgents: 'Agents',
    navDevices: 'Devices',
    navChannels: 'Channels',
    navTerminal: 'Terminal',
    navLogs: 'Logs',
    metricAgents: 'Agents',
    metricPending: 'Pending',
    metricOnlineChannels: 'Online channels',
    metricBusy: 'Busy',
    filterChannels: 'Filter channels',
    refresh: 'Refresh',
    refreshing: 'Refreshing',
    updatedJustNow: 'Updated just now',
    apiUnavailable: 'API unavailable',
    loadingAPI: 'Loading API',
    apiConnected: 'API connected',
    apiError: 'API error',
    language: 'Language'
  },
  'zh-CN': {
    appName: '串口平台',
    centralServer: 'central-server',
    navAgents: 'Agents',
    navDevices: 'Devices',
    navChannels: 'Channels',
    navTerminal: 'Terminal',
    navLogs: 'Logs',
    metricAgents: 'Agent',
    metricPending: '待确认',
    metricOnlineChannels: '在线 channel',
    metricBusy: '占用',
    filterChannels: '筛选 channel',
    refresh: '刷新',
    refreshing: '刷新中',
    updatedJustNow: '刚刚更新',
    apiUnavailable: 'API 不可用',
    loadingAPI: '正在加载 API',
    apiConnected: 'API 已连接',
    apiError: 'API 错误',
    language: '语言'
  }
} satisfies Record<Language, Record<string, string>>;

export type MessageKey = keyof typeof messages.en;

export function detectDefaultLanguage(): Language {
  const stored = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
  if (stored === 'en' || stored === 'zh-CN') {
    return stored;
  }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en';
}
