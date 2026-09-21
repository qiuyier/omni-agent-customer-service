import { normalizeStorage, normalizeReplyMode } from './protocol.mjs';

const presentations = {
  memory: {
    label: '内存演示',
    description: '消息仅保存在服务内存，重启后清空。模拟登录不是真实认证。',
  },
  postgres: {
    label: '持久化演示',
    description: '记录保存在 PostgreSQL；服务重启后重新选择同一演示账号，历史仍保留。模拟登录不是真实认证。',
  },
  unknown: {
    label: '接入演示',
    description: '尚未确认服务端存储方式，请勿依赖演示记录保留。模拟登录不是真实认证。',
  },
};

const replies = {
  fake: {
    label: '模拟回复',
    capabilities: '助手使用固定逻辑生成模拟回复，未接入真实模型。不提供业务查询、业务执行或人工转接。',
    emptyDescription: '这里还没有消息。写下第一个问题，试试聊天流程。回复为模拟内容，不代表真实服务。',
  },
  ai: {
    label: 'AI 回复',
    capabilities: '助手根据对话和已提供的资料生成 AI 回复，回答需核实。不提供业务查询、业务执行或人工转接。',
    emptyDescription: '这里还没有消息。可以围绕接入方提供的知识资料提问。AI 回答需核实，不代表业务查询或操作结果。',
  },
  unknown: {
    label: '回复模式未确认',
    capabilities: '尚未确认服务端回复模式，无法判断回复来源。不提供业务查询、业务执行或人工转接。',
    emptyDescription: '这里还没有消息。可发送问题测试聊天流程；回复模式尚未确认，请核实回复内容。',
  },
};

// Capability and persistence claims require explicit, separately validated server metadata.
export function demoPresentation(storage, replyMode) {
  const persistence = presentations[normalizeStorage(storage) || 'unknown'];
  const reply = replies[normalizeReplyMode(replyMode) || 'unknown'];
  return { ...persistence, label: `${persistence.label} · ${reply.label}`, capabilities: reply.capabilities, emptyDescription: reply.emptyDescription };
}
