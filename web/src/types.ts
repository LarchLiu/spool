import type { AvatarConfig } from "./avatar";

export type Backend = "codex" | "claude";
export type User = {
  id: string;
  username: string;
  nickname: string;
  avatar?: AvatarConfig;
};
export type Project = {
  id: string;
  name: string;
  archived: boolean;
  shared_chats: boolean;
};
export type Effort = { reasoningEffort: string; description: string };
export type Model = {
  model: string;
  displayName: string;
  defaultReasoningEffort: string;
  supportedReasoningEfforts: Effort[];
};
export type Catalog = {
  models: Model[];
  defaultModel: string;
  defaultEffort: string;
};
export type Agent = {
  backend: Backend;
  enabled: boolean;
  installed: boolean;
  version: string;
  catalog: Catalog;
  error?: string;
  checked_at: number;
};
export type Attachment = {
  id: string;
  name: string;
  media_type: string;
  size: number;
  preview_url?: string;
  excerpt?: string;
};
export type PermissionCatalog = {
  backend: Backend;
  default: string;
  current: string;
  modes: { id: string; available: boolean; reason?: string }[];
  approval_policy?: string;
  sandbox?: string;
};
export type ApprovalRequest = {
  id: string;
  title: string;
  detail?: string;
  reason?: string;
};
export type TranscriptPart = {
  role: "user" | "assistant" | "tool";
  kind: "text" | "thinking" | "tool_use" | "tool_result" | "context" | "image";
  name?: string;
  text: string;
  cut?: number;
  model?: string;
};
export type Transcript = {
  parts: TranscriptPart[];
  cut?: boolean;
  source?: string;
  captured?: number;
};
export type Turn = {
  id: string;
  backend?: Backend;
  part_start: number;
  input_end: number;
  part_end: number;
  started_at: number;
  finished_at?: number;
  status: string;
  model?: string;
  requested_model?: string;
  reasoning_effort?: string;
  error?: string;
  permission_mode?: string;
  attachments?: Attachment[];
  pending_approval?: ApprovalRequest;
};
export type Chat = {
  automation_chat?: boolean;
  collaborator_ids?: string[];
  artifacts?: { type: "pull_request"; url: string }[];
  id: string;
  user_id?: string;
  project_id: string;
  title: string;
  backend: Backend;
  status: string;
  model?: string;
  reasoning_effort?: string;
  permission_mode?: string;
  transcript: Transcript;
  turns: Turn[];
  updated_at: number;
};
export type Online = {
  id: string;
  nickname: string;
  state: string;
  devices: number;
  avatar?: AvatarConfig;
};
