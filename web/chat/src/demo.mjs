import { normalizeStorage } from './protocol.mjs';

const presentations = {
  memory: {
    label: '内存演示 · 模拟回复',
    description: '消息仅保存在服务内存，重启后清空。模拟登录不是真实认证。',
  },
  postgres: {
    label: '持久化演示 · 模拟回复',
    description: '记录保存在 PostgreSQL；服务重启后重新选择同一演示账号，历史仍保留。模拟登录不是真实认证。',
  },
  unknown: {
    label: '接入演示 · 模拟回复',
    description: '尚未确认服务端存储方式，请勿依赖演示记录保留。模拟登录不是真实认证。',
  },
};

// Persistence claims are based only on the server's explicit storage metadata.
export const demoPresentation = (storage) => presentations[normalizeStorage(storage) || 'unknown'];
