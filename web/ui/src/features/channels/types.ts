/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
// Modified 2026-11-06 by new-api-sub: rows of our GET /api/channels (no key, no remark) plus exists/deleted_at/owner/balance_from_keys/template_id; keys of a multi-key channel by number; one type entry per enabled template; request types of our endpoints
// ============================================================================
// Channel Types
// ============================================================================

export type ChannelInfo = {
  is_multi_key?: boolean
  multi_key_size?: number
  multi_key_status_list?: Record<string, number> | null
  multi_key_mode?: 'random' | 'polling'
}

export type ChannelOwner = {
  /** 0 or null means the admin uploaded this key himself. */
  user_id?: number | null
  username?: string
  admin_id: number
  admin_username: string
}

export type Channel = {
  id: number
  type: number
  name: string
  status: number // 1: enabled, 2: manual disabled, 3: auto disabled
  group: string
  models: string
  priority?: number | null
  weight?: number | null
  used_quota: number
  balance: number // in USD
  balance_updated_time: number
  /** Multi-key channels: balance is the sum of the keys' stored balances. */
  balance_from_keys?: boolean
  response_time: number // in milliseconds
  test_time: number
  created_time: number
  base_url?: string | null
  tag?: string | null
  test_model?: string | null
  auto_ban?: number | null
  channel_info?: ChannelInfo | null
  other_info?: string | null
  /** False when the channel no longer exists in new-api (orphan). */
  exists: boolean
  /** Unix seconds; set while soft-deleted. */
  deleted_at?: number | null
  /** Omitted in the portal. */
  owner?: ChannelOwner
  /** The template it was created from; null for older channels. */
  template_id: number | null
  push_state: ChannelPushState
  /** One per pool the channel goes to; null or empty before any exists. */
  placements: ChannelPlacement[] | null
  /** Disabled, while some pool still has it enabled until the push gets there. */
  status_pending?: boolean
}

/** Of all the pools a channel goes to: none created yet, some, refused, all. */
export type ChannelPushState = 'pending' | 'partial' | 'failed' | 'done'

/**
 * The channel in one pool. The portal gets the state and pool; the error is for
 * admins, the new-api id for a super.
 */
export type ChannelPlacement = {
  state: 'pending' | 'pushing' | 'created' | 'failed'
  pool_id?: number
  pool_name?: string
  error?: string
  newapi_channel_id?: number | null
}

/**
 * portal: a supplier; admin: a regular admin; super. Channels of every role
 * take the type's fixed parameters; admin and super can also choose the owner.
 */
export type ChannelsMode = 'portal' | 'admin' | 'super'

export type ChannelView = 'active' | 'deleted'

/** Passed to new-api as they are; an empty value means not set. */
export type ChannelExtra = {
  other?: string
  setting?: string
  settings?: string
  model_mapping?: string
  status_code_mapping?: string
  header_override?: string
  param_override?: string
  auto_ban?: number
  tag?: string
  openai_organization?: string
}

/**
 * What a portal user or regular admin gets of a template's extra; the rest
 * stays on the server. Optional fields are left out when not set.
 */
export type PublicChannelExtra = {
  auto_ban?: number
  tag?: string
  model_mapping?: string
  status_code_mapping?: string
  other_set: boolean
  vertex_key_type?: 'json' | 'api_key'
  aws_key_type?: 'ak_sk' | 'api_key'
}

/** One enabled template; a type with several templates has several of these. */
export type ChannelTypeDefaults = {
  template_id: number
  template_name: string
  type: number
  base_url: string
  allow_custom_base_url: boolean
  models: string
  group: string
  priority: number
  weight: number
  test_model: string
  /** The full extra for a super only. */
  extra?: ChannelExtra | PublicChannelExtra | null
}

/** What the template form edits; the admin app's template rows have this shape. */
export type ChannelTemplateValues = {
  name: string
  channel_type: number
  base_url: string
  allow_custom_base_url: boolean
  models: string
  group: string
  priority: number
  weight: number
  test_model: string
  extra: ChannelExtra | null
  enabled: boolean
  /** Pools a new channel is pushed to, in this order; at least one. */
  pool_ids: number[]
}

/** What the create form reads of a channel: the fixed parameters of its type. */
export type ChannelFormSource = ChannelExtra & {
  type: number
  status: number
  name?: string
  base_url?: string
  models?: string
  group?: string
  priority?: number
  weight?: number
  test_model?: string
  remark?: string
}

export type ChannelOwnerOption = {
  id: number
  username: string
  /** Only for a super, who sees every admin's users. */
  admin_username?: string
}

// ============================================================================
// Channel Settings Types
// ============================================================================

export interface ChannelSettings {
  task_plugin_key?: string
  task_extend_plugin_keys?: string[]
  force_format?: boolean
  thinking_to_content?: boolean
  proxy?: string
  pass_through_body_enabled?: boolean
  responses_websocket_enabled?: boolean
  system_prompt?: string
  system_prompt_override?: boolean
  http_protocol?: 'auto' | 'http1' | string
  http2_connection_shards?: number
  tls_insecure_skip_verify?: boolean
}

export interface ChannelOtherSettings {
  azure_responses_version?: string
  vertex_key_type?: 'json' | 'api_key'
  vertex_project_id?: string
  vertex_api_version?: 'v1' | 'v1beta1'
  openrouter_enterprise?: boolean
  aws_key_type?: 'ak_sk' | 'api_key'
  aws_api_type?: 'invoke_model' | 'mantle'
  allow_service_tier?: boolean
  disable_store?: boolean
  allow_safety_identifier?: boolean
  allow_include_obfuscation?: boolean
  allow_inference_geo?: boolean
  allow_speed?: boolean
  claude_beta_query?: boolean
  ollama_openai_chat?: boolean
  disable_task_polling_sleep?: boolean
  upstream_model_update_check_enabled?: boolean
  upstream_model_update_auto_sync_enabled?: boolean
  upstream_model_update_ignored_models?: string[]
  upstream_model_update_last_check_time?: number
  upstream_model_update_last_detected_models?: string[]
  advanced_custom?: AdvancedCustomConfig
}

export interface AdvancedCustomConfig {
  advanced_routes?: AdvancedCustomRoute[]
}

export interface AdvancedCustomRoute {
  incoming_path?: string
  upstream_path?: string
  converter?: AdvancedCustomConverter
  models?: string[]
  auth?: AdvancedCustomRouteAuth
  pass_through_body_enabled?: boolean
}

export interface AdvancedCustomRouteAuth {
  type?: AdvancedCustomAuthType
  name?: string
  value?: string
}

export type AdvancedCustomConverter =
  | 'none'
  | 'anthropic_messages_to_openai_chat_completions'
  | 'openai_chat_completions_to_anthropic_messages'
  | 'openai_chat_completions_to_openai_responses'
  | 'openai_responses_to_openai_chat_completions'
  | 'openai_responses_to_gemini_generate_content'
  | 'gemini_generate_content_to_openai_chat_completions'
  | 'openai_chat_completions_to_gemini_generate_content'
  | 'claude_messages_to_openai_responses'
  | 'gemini_generate_content_to_openai_responses'

export type AdvancedCustomAuthType = 'none' | 'header' | 'query'

// ============================================================================
// API Response Types
// ============================================================================

export interface ChannelTestResponse {
  success: boolean
  message?: string
  /** Seconds, as returned by new-api. */
  time?: number
}

/** One key of a multi-key channel, by position only; the key itself never leaves the server. */
export type ChannelKey = {
  /** From 0; shown as index + 1. */
  index: number
  status: number // 1: enabled, 2: manual disabled, 3: auto disabled
  disabled_time?: number | null
  reason?: string | null
  /** USD. */
  balance: number | null
  balance_at: number | null
  balance_error?: string
}

export type ChannelKeys = {
  /** The super's switch for testing and querying the balance of a key. */
  probe_enabled: boolean
  /** Whether the channel's type and address allow it. */
  probe_supported: boolean
  total_balance: number | null
  keys: ChannelKey[]
}

export type ChannelKeyTestResult = {
  index: number
  ok: boolean
  message: string
  latency_ms: number
}

// ============================================================================
// API Request Parameters
// ============================================================================

export type ChannelSortBy =
  | 'id'
  | 'name'
  | 'priority'
  | 'balance'
  | 'response_time'
  | 'test_time'

// ============================================================================
// Add Channel Request
// ============================================================================

export interface CreateChannelPayload {
  type: number
  name: string
  key: string
  base_url: string
  mode: 'single' | 'batch' | 'multi_to_single'
  multi_key_mode?: 'random' | 'polling'
  /** Admin and super only; 0 keeps the channel with the admin himself. */
  user_id?: number
  /** A subset of the template's models. */
  models: string
  /** Required when the type has several enabled templates. */
  template_id?: number
}
