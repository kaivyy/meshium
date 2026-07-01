import { api } from '$lib/api/client';

export type AssistantRole = 'user' | 'assistant' | 'system';

export interface ChatMessage {
  role: AssistantRole;
  content: string;
  timestamp?: string;
}

export interface ChatRequest {
  message: string;
  serverId?: number;
  history?: ChatMessage[];
}

export interface ChatResponse {
  message: string;
  suggestions: string[];
  commands: string[];
  context?: string;
}

export interface LogAnalysis {
  explanation: string;
  severity: 'low' | 'medium' | 'high' | 'critical';
  suggestions: string[];
}

export interface CommandHelp {
  command: string;
  description: string;
  examples: string[];
  warning?: string;
}

export interface SuggestionsResponse {
  suggestions: string[];
}

export const assistantApi = {
  chat: (payload: ChatRequest) => api.post<ChatResponse>('/assistant/chat', payload),
  suggestions: (serverId?: number) =>
    api.get<SuggestionsResponse>(
      `/assistant/suggestions${typeof serverId === 'number' && serverId > 0 ? `?serverId=${serverId}` : ''}`
    ),
  analyzeLog: (payload: { serverId?: number; logContent: string }) =>
    api.post<LogAnalysis>('/assistant/analyze-log', payload),
  commandHelp: (command: string) => api.post<CommandHelp>('/assistant/command-help', { command })
};
