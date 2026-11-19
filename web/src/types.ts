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
  shared_local_chats?: boolean;
  allow_external_files?: boolean;
  can_create?: boolean;
};
export type ProjectFileEntry = {
  name: string;
  path: string;
  kind: "file" | "directory";
  size?: number;
};
export type SessionWorkspace = {
  path: string;
  branch?: string;
  commit?: string;
  kind: "main" | "worktree" | "directory";
  available: boolean;
};
export type SessionWorkspaceView = SessionWorkspace & {
  workspaces?: SessionWorkspace[];
  turn_id?: string;
};
export type ProjectDirectory = {
  path: string;
  entries: ProjectFileEntry[];
  truncated: boolean;
};
export type ProjectFile = {
  preview_url?: string;
  download_url?: string;
  external?: boolean;
  path: string;
  name: string;
  size: number;
  kind: "text" | "image" | "binary";
  media_type: string;
  content: string | null;
  truncated: boolean;
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
  commands?: AgentCommand[];
  commandSource?: "native" | "protocol";
};
export type AgentCommand = {
  name: string;
  displayName?: string;
  description: string;
  argumentHint?: string;
  aliases?: string[];
  builtin: boolean;
  kind: "control" | "prompt" | "client" | "skill";
  scope?: string;
  pluginId?: string;
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
  download_url?: string;
  excerpt?: string;
  file_path?: string;
};
export type PermissionCatalog = {
  backend: Backend;
  default: string;
  current: string;
  modes: { id: string; available: boolean; reason?: string }[];
  approval_policy?: string;
  sandbox?: string;
  commands?: AgentCommand[];
};
export type ApprovalRequest = {
  id: string;
  title: string;
  detail?: string;
  reason?: string;
  url?: string;
  fields?: ApprovalField[];
  kind?: "input";
  decisions?: ApprovalDecision[];
  schema?: Record<string, unknown>;
  json_form?: boolean;
  feedback?: boolean;
  unavailable?: string;
};
export type ApprovalDecision = {
  id: string;
  label: string;
  description?: string;
  kind: "approve" | "deny" | "cancel" | "submit";
};
export type ApprovalField = {
  name: string;
  label: string;
  description?: string;
  type: "string" | "boolean" | "integer" | "number" | "array" | "json";
  format?: string;
  required?: boolean;
  options?: { value: string; label: string; description?: string }[];
  default?: unknown;
  minimum?: number;
  maximum?: number;
  min_length?: number;
  max_length?: number;
  min_items?: number;
  max_items?: number;
  allow_other?: boolean;
  secret?: boolean;
  multiline?: boolean;
  step?: number;
};
export type TranscriptPart = {
  client_message_id?: string;
  id?: string;
  timestamp?: number;
  // A steering message its model has yet to take in; it may be withdrawn.
  pending?: boolean;
  phase?: "commentary" | "final_answer";
  role: "user" | "assistant" | "tool";
  kind: "text" | "thinking" | "tool_use" | "tool_result" | "context" | "image";
  image_url?: string;
  file_path?: string;
  images?: string[];
  name?: string;
  text: string;
  cut?: number;
  model?: string;
  tool?: ToolSummary;
};
export type ToolSummary = {
  ref?: string;
  href?: string;
  version: string;
  bytes: number;
  kind:
    | "read"
    | "write"
    | "create"
    | "delete"
    | "search"
    | "list"
    | "web"
    | "shell"
    | "thinking"
    | "image"
    | "task"
    | "other";
  path?: string;
  image_paths?: string[];
  image_count?: number;
  image_urls?: string[];
  query?: string;
  preview?: string;
  stats?: string;
  changes?: { additions: number; deletions: number };
  operations?: ToolOperation[];
  // What the provider reported about the call. A status may arrive on the
  // call or on its result; absent means the provider did not say.
  status?: "running" | "completed" | "failed" | "declined" | "stopped" | "unknown";
  exit_code?: number;
  duration_ms?: number;
  files?: ToolFile[];
  task?: {
    id: string;
    parent_id?: string;
    agent_type?: string;
    background?: boolean;
    summary?: string;
    last_tool?: string;
    tool_uses?: number;
    tokens?: number;
  };
};
// Display-only input summaries. Compound commands have no per-operation output.
export type ToolOperation = {
  kind: ToolSummary["kind"];
  command: string;
  path?: string;
  query?: string;
};
export type ToolFile = {
  path: string;
  kind: "create" | "write" | "delete";
  move_path?: string;
  additions: number;
  deletions: number;
};
export type Transcript = {
  parts: TranscriptPart[];
  cut?: boolean;
  source?: string;
  captured?: number;
};
export type Turn = {
  id: string;
  // Client-only identity retained when optimistic/live IDs become server/native IDs.
  render_key?: string;
  user_id?: string;
  actions?: TurnAction[];
  native_turn_id?: string;
  native_prompt_id?: string;
  backend?: Backend;
  part_start: number;
  input_end: number;
  part_end: number;
  started_at: number;
  finished_at?: number;
  status: string;
  compacting?: boolean;
  model?: string;
  requested_model?: string;
  reasoning_effort?: string;
  error?: string;
  permission_mode?: string;
  attachments?: Attachment[];
  pending_approval?: ApprovalRequest;
};
export type TurnAction = {
  id: string;
  user_id: string;
  kind: "stop" | "approve" | "deny" | "cancel" | "respond";
  approval_id?: string;
  summary?: string;
  created_at: number;
};
export type Chat = {
  local_session?: boolean;
  can_control?: boolean;
  participants?: { id: string }[];
  history?: { before?: string; has_more?: boolean; error?: string };
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
  queued_messages?: QueuedMessage[];
};
export type QueuedMessage = {
  id: string;
  client_message_id?: string;
  delivery_status?: "sending" | "failed";
  user_id?: string;
  message: string;
  attachments?: string[];
  created_at: number;
};
export type ChatMetadata = Pick<
  Chat,
  | "id"
  | "updated_at"
  | "automation_chat"
  | "collaborator_ids"
  | "artifacts"
  | "participants"
  | "can_control"
> & { title?: string };
export type Online = {
  id: string;
  state: string;
  devices: number;
};
