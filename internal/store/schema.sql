CREATE TABLE IF NOT EXISTS admin_users (
  id BIGSERIAL PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('super', 'admin')),
  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (1, 2)),
  session_version INT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now());

CREATE TABLE IF NOT EXISTS portal_users (
  id BIGSERIAL PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  admin_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (1, 2)),
  session_version INT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS portal_users_admin_id_idx ON portal_users(admin_id);

CREATE TABLE IF NOT EXISTS channel_templates (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  channel_type INT NOT NULL CHECK (channel_type > 0),
  base_url TEXT NOT NULL DEFAULT '',
  allow_custom_base_url BOOLEAN NOT NULL DEFAULT false,
  models TEXT NOT NULL,
  group_name TEXT NOT NULL,
  priority BIGINT NOT NULL DEFAULT 0,
  weight INT NOT NULL DEFAULT 0 CHECK (weight >= 0),
  test_model TEXT NOT NULL DEFAULT '',
  extra JSONB NOT NULL DEFAULT '{}',
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
-- A channel type may have several enabled templates.
DROP INDEX IF EXISTS channel_templates_enabled_type_idx;
-- Also makes a database from before this column fail at startup, not on first use.
COMMENT ON COLUMN channel_templates.extra IS 'Passed through to new-api and shown to the users of the type: no secrets.';

CREATE TABLE IF NOT EXISTS portal_channels (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT REFERENCES portal_users(id) ON DELETE RESTRICT,
  admin_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
  newapi_channel_id INT NOT NULL UNIQUE,
  template_id BIGINT REFERENCES channel_templates(id) ON DELETE SET NULL,
  name TEXT NOT NULL,
  deleted_at TIMESTAMPTZ,
  deleted_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS portal_channels_user_id_idx ON portal_channels(user_id);
CREATE INDEX IF NOT EXISTS portal_channels_admin_id_idx ON portal_channels(admin_id);

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGSERIAL PRIMARY KEY,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('portal', 'admin')),
  actor_id BIGINT,
  actor_name TEXT NOT NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  success BOOLEAN NOT NULL,
  detail JSONB NOT NULL DEFAULT '{}',
  ip TEXT NOT NULL DEFAULT '',
  forwarded_for TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS audit_logs_created_idx ON audit_logs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS audit_logs_actor_idx ON audit_logs(actor_type, actor_id, id DESC);
-- text_pattern_ops also serves the prefix search whatever the database's collation.
CREATE INDEX IF NOT EXISTS audit_logs_ip_idx ON audit_logs(ip text_pattern_ops, id DESC);

-- A missing row is an error when read, so a lost setting cannot widen what anyone sees.
CREATE TABLE IF NOT EXISTS app_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
INSERT INTO app_settings (key, value) VALUES
  ('log_scope', 'all'),
  ('portal_name', '小蓝号池'),
  ('admin_name', '小蓝号池管理后台'),
  ('portal_public_url', ''),
  ('key_probe', 'off')
  ON CONFLICT (key) DO NOTHING;

-- Empty follows app_settings.log_scope.
ALTER TABLE portal_users ADD COLUMN IF NOT EXISTS log_scope TEXT NOT NULL DEFAULT '' CHECK (log_scope IN ('', 'all', 'self'));

CREATE TABLE IF NOT EXISTS app_logo (
  id BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
  content_type TEXT NOT NULL,
  data BYTEA NOT NULL CHECK (octet_length(data) <= 262144),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());

-- Append-only: a correction is a new row, and the latest row is what counts.
CREATE TABLE IF NOT EXISTS settlements (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES portal_users(id) ON DELETE RESTRICT,
  amount_usd NUMERIC(18, 6) NOT NULL CHECK (amount_usd >= 0),
  note TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS settlements_user_idx ON settlements(user_id, id DESC);

-- The last OpenRouter balance of each key of a multi-key channel, by its index in new-api's key list.
-- balance is NULL when the last check failed, and error says why (never the key).
CREATE TABLE IF NOT EXISTS channel_key_balances (
  channel_id INT NOT NULL REFERENCES portal_channels(newapi_channel_id) ON DELETE CASCADE,
  key_index INT NOT NULL CHECK (key_index >= 0),
  balance NUMERIC,
  error TEXT NOT NULL DEFAULT '',
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, key_index));

-- What a portal user's programs sign in with. Only the SHA-256 of a token is kept; revoking deletes the row.
CREATE TABLE IF NOT EXISTS api_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES portal_users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  hint TEXT NOT NULL,
  expires_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS api_tokens_user_id_idx ON api_tokens(user_id);

-- Short-lived state of a login, a passkey ceremony or a re-verification; a row lives 10 minutes at most.
-- No foreign key: account ids are never reused, and an older release must still be able to delete users.
CREATE TABLE IF NOT EXISTS auth_flows (
  token_hash TEXT PRIMARY KEY,
  realm TEXT NOT NULL CHECK (realm IN ('portal_users', 'admin_users')),
  kind TEXT NOT NULL CHECK (kind IN ('login', 'passkey_login', 'passkey_proof', 'totp_setup', 'passkey_setup', 'proof', 'captcha')),
  account_id BIGINT NOT NULL DEFAULT 0,
  session_version INT NOT NULL DEFAULT 0,
  purpose TEXT NOT NULL DEFAULT '',
  data BYTEA,
  expires_at TIMESTAMPTZ NOT NULL);
CREATE INDEX IF NOT EXISTS auth_flows_expires_idx ON auth_flows(expires_at);

-- lookup is a passkey's credential id or a backup code's SHA-256; secret is a TOTP ciphertext or a passkey's JSON.
-- CASCADE, not RESTRICT: an older release knows nothing of this table and still deletes users.
CREATE TABLE IF NOT EXISTS auth_factors (
  id BIGSERIAL PRIMARY KEY,
  portal_user_id BIGINT REFERENCES portal_users(id) ON DELETE CASCADE,
  admin_user_id BIGINT REFERENCES admin_users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('totp', 'passkey', 'backup')),
  lookup BYTEA,
  secret BYTEA,
  last_step BIGINT NOT NULL DEFAULT 0,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (num_nonnulls(portal_user_id, admin_user_id) = 1));
CREATE UNIQUE INDEX IF NOT EXISTS auth_factors_portal_kind_idx ON auth_factors(portal_user_id, kind) WHERE kind <> 'backup';
CREATE UNIQUE INDEX IF NOT EXISTS auth_factors_admin_kind_idx ON auth_factors(admin_user_id, kind) WHERE kind <> 'backup';
CREATE UNIQUE INDEX IF NOT EXISTS auth_factors_credential_idx ON auth_factors(lookup) WHERE kind = 'passkey';
CREATE INDEX IF NOT EXISTS auth_factors_portal_idx ON auth_factors(portal_user_id);
CREATE INDEX IF NOT EXISTS auth_factors_admin_idx ON auth_factors(admin_user_id);

-- The connection to new-api. Everything but the address is AES-GCM ciphertext under APP_MASTER_KEY, so it stays out
-- of app_settings, which GET /api/settings echoes. Empty log_dsn follows db_dsn, empty key_dsn turns key reads off.
CREATE TABLE IF NOT EXISTS newapi_connection (
  id BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
  base_url TEXT NOT NULL,
  pat BYTEA NOT NULL,
  db_dsn BYTEA NOT NULL,
  log_dsn BYTEA,
  key_dsn BYTEA,
  version BIGINT NOT NULL DEFAULT 1,
  updated_by TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());

-- The new-api instances channels go to. Everything but the address is AES-GCM ciphertext under APP_MASTER_KEY bound
-- to pool:<id>:<field>; aad_legacy marks pool 1 as copied from newapi_connection, under that table's field names,
-- until the next start seals it again. newapi_connection stays as it was for a rollback to the release before pools.
CREATE TABLE IF NOT EXISTS newapi_pools (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 40),
  base_url TEXT NOT NULL,
  pat BYTEA NOT NULL,
  db_dsn BYTEA,
  log_dsn BYTEA,
  key_dsn BYTEA,
  aad_legacy BOOLEAN NOT NULL DEFAULT false,
  log_mode TEXT NOT NULL DEFAULT 'direct' CHECK (log_mode IN ('direct', 'sync')),
  accept_new BOOLEAN NOT NULL DEFAULT true, -- takes new channels; reads and existing channels go on either way
  quota_per_unit DOUBLE PRECISION CHECK (quota_per_unit > 0), -- as /api/status last said, for when the pool is down
  version BIGINT NOT NULL DEFAULT 1,
  updated_by TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (log_mode = 'sync' OR db_dsn IS NOT NULL));
CREATE UNIQUE INDEX IF NOT EXISTS newapi_pools_base_url_idx ON newapi_pools (lower(base_url));

-- Pool 1 is the connection of before, once: pool ids are never reused, and the sequence says whether any was taken.
DO $$ BEGIN
  IF NOT (SELECT is_called FROM newapi_pools_id_seq) AND EXISTS (SELECT 1 FROM newapi_connection) THEN
    INSERT INTO newapi_pools (id, name, base_url, pat, db_dsn, log_dsn, key_dsn, aad_legacy, updated_by)
      SELECT 1, '号池 1', base_url, pat, db_dsn, log_dsn, key_dsn, true, 'migrate' FROM newapi_connection;
    PERFORM setval('newapi_pools_id_seq', 1);
  END IF;
END $$;

-- What every pool gets of a channel, without key and remark; NULL for channels from before pools.
ALTER TABLE portal_channels ADD COLUMN IF NOT EXISTS spec JSONB;
-- The key as submitted, sealed to channel:<id>:key. Only the push worker reads it; nothing returns it.
ALTER TABLE portal_channels ADD COLUMN IF NOT EXISTS key_sealed BYTEA;
ALTER TABLE portal_channels ADD COLUMN IF NOT EXISTS want_status SMALLINT NOT NULL DEFAULT 1 CHECK (want_status IN (1, 2));
COMMENT ON COLUMN portal_channels.newapi_channel_id IS 'The channel id here and in every API. A channel from before pools (spec NULL) keeps its id in pool 1; newer ones take portal_channel_ids. Where it is in each pool: channel_placements.';

-- Local ids of channels from here on: past every id taken, and from 10000. Moved past the ids a release before
-- pools adds meanwhile (a rollback), which are pool 1 ids.
DO $$ DECLARE m INT; BEGIN
  SELECT COALESCE(max(newapi_channel_id), 0) INTO m FROM portal_channels;
  IF to_regclass('portal_channel_ids') IS NULL THEN
    EXECUTE format('CREATE SEQUENCE portal_channel_ids AS INT MINVALUE 10000 START %s', greatest(10000, m + 1));
  ELSIF m >= (SELECT last_value FROM portal_channel_ids) THEN
    PERFORM setval('portal_channel_ids', m);
  END IF;
END $$;

ALTER TABLE channel_templates ADD COLUMN IF NOT EXISTS pool_ids BIGINT[] NOT NULL DEFAULT '{}';

-- A channel in one pool: the new-api channel it is there, and the push worker's state for it.
CREATE TABLE IF NOT EXISTS channel_placements (
  channel_id INT NOT NULL REFERENCES portal_channels(newapi_channel_id) ON DELETE CASCADE,
  pool_id BIGINT NOT NULL REFERENCES newapi_pools(id) ON DELETE RESTRICT,
  seq SMALLINT NOT NULL DEFAULT 0, -- order in the template's pool_ids: of pushing, and of which pool leads
  newapi_channel_id INT,
  state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'pushing', 'created', 'failed')),
  remark TEXT NOT NULL DEFAULT '', -- one-time value to find the new-api channel by; empty for channels from before pools
  attempts INT NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- also the lease of a claimed job
  attempt_started_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '', -- a fixed text or new-api's refusal, masked
  status_dirty BOOLEAN NOT NULL DEFAULT false,
  config_dirty BOOLEAN NOT NULL DEFAULT false,
  keys_dirty BOOLEAN NOT NULL DEFAULT false,
  state_json JSONB, -- the channel row of a pool without a database, as last read
  state_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, pool_id),
  CHECK ((state = 'created') = (newapi_channel_id IS NOT NULL)));
CREATE UNIQUE INDEX IF NOT EXISTS channel_placements_newapi_idx ON channel_placements(pool_id, newapi_channel_id) WHERE newapi_channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS channel_placements_work_idx ON channel_placements(next_attempt_at)
  WHERE state IN ('pending', 'pushing') OR status_dirty OR config_dirty OR keys_dirty;

-- Templates and channels from before pools, or added by such a release meanwhile, are pool 1's. The same two
-- statements run where pool 1 is imported (store/pools.go).
UPDATE channel_templates SET pool_ids = '{1}' WHERE pool_ids = '{}' AND EXISTS (SELECT 1 FROM newapi_pools WHERE id = 1);
INSERT INTO channel_placements (channel_id, pool_id, newapi_channel_id, state, created_at)
  SELECT c.newapi_channel_id, 1, c.newapi_channel_id, 'created', c.created_at FROM portal_channels c
  WHERE c.spec IS NULL AND EXISTS (SELECT 1 FROM newapi_pools WHERE id = 1)
    AND NOT EXISTS (SELECT 1 FROM channel_placements p WHERE p.channel_id = c.newapi_channel_id)
  ON CONFLICT DO NOTHING;

-- Syncing a pool's logs into MongoDB. The controls (enabled to max_windows) restart nothing: the worker reads them
-- every round. cursor: every log before it, in Unix seconds, was read. max_log_id and max_log_at are the newest log
-- id and created_at since the last reset, against which a smaller id is caught; paused is why the worker stopped.
CREATE TABLE IF NOT EXISTS newapi_pool_sync (
  pool_id BIGINT PRIMARY KEY REFERENCES newapi_pools(id) ON DELETE CASCADE,
  enabled BOOLEAN NOT NULL DEFAULT false,
  interval_s INT NOT NULL DEFAULT 60 CHECK (interval_s BETWEEN 30 AND 3600),
  window_s INT NOT NULL DEFAULT 600 CHECK (window_s BETWEEN 60 AND 3600),
  max_windows INT NOT NULL DEFAULT 6 CHECK (max_windows BETWEEN 1 AND 60),
  cursor BIGINT NOT NULL,
  max_log_id BIGINT NOT NULL DEFAULT 0,
  max_log_at BIGINT NOT NULL DEFAULT 0,
  paused TEXT NOT NULL DEFAULT '',
  failures INT NOT NULL DEFAULT 0,
  backoff_until TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  last_ok_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '', -- a fixed text
  last_rows INT NOT NULL DEFAULT 0,
  last_ms INT NOT NULL DEFAULT 0,
  rows_total BIGINT NOT NULL DEFAULT 0,
  newest_log_at BIGINT NOT NULL DEFAULT 0,
  groups TEXT[] NOT NULL DEFAULT '{}', -- the groups of a pool without a database, as last read
  channels_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());

-- What the log sync found wrong. detail holds created_at, request_id, both fingerprints or the other pool; never content.
CREATE TABLE IF NOT EXISTS log_sync_conflicts (
  id BIGSERIAL PRIMARY KEY,
  pool_id BIGINT NOT NULL REFERENCES newapi_pools(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('fingerprint', 'id_regression', 'duplicate_source', 'zero_id', 'clock_skew')),
  log_id BIGINT NOT NULL DEFAULT 0,
  detail JSONB NOT NULL DEFAULT '{}',
  count INT NOT NULL DEFAULT 1,
  first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TIMESTAMPTZ);
CREATE UNIQUE INDEX IF NOT EXISTS log_sync_conflicts_open_idx ON log_sync_conflicts(pool_id, kind, log_id) WHERE resolved_at IS NULL;
