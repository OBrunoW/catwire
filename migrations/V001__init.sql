CREATE SCHEMA IF NOT EXISTS catwire;

CREATE TABLE IF NOT EXISTS catwire.schema_migrations (
  version text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION catwire.set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

CREATE TABLE IF NOT EXISTS catwire.instances (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  client_id text NOT NULL,
  external_id text,
  name text NOT NULL,
  status text NOT NULL DEFAULT 'created'
    CHECK (status IN ('created','connecting','qr_required','connected','disconnected','logged_out','banned','client_outdated')),
  status_reason text,
  auto_connect boolean NOT NULL DEFAULT true,
  jid text,
  phone text,
  lid text,
  push_name text,
  business_name text,
  platform text,
  connected_at timestamptz,
  disconnected_at timestamptz,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS instances_client_external_uq
  ON catwire.instances (client_id, external_id)
  WHERE external_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS instances_client_created_idx
  ON catwire.instances (client_id, created_at DESC, id DESC)
  WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS instances_jid_idx
  ON catwire.instances (jid)
  WHERE jid IS NOT NULL AND deleted_at IS NULL;

DROP TRIGGER IF EXISTS instances_set_updated_at ON catwire.instances;
CREATE TRIGGER instances_set_updated_at BEFORE UPDATE ON catwire.instances
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();

CREATE TABLE IF NOT EXISTS catwire.instance_settings (
  instance_id uuid PRIMARY KEY REFERENCES catwire.instances (id) ON DELETE CASCADE,
  sync_history boolean NOT NULL DEFAULT true,
  ignore_groups boolean NOT NULL DEFAULT false,
  auto_mark_read boolean NOT NULL DEFAULT false,
  always_online boolean NOT NULL DEFAULT false,
  download_media boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS instance_settings_set_updated_at ON catwire.instance_settings;
CREATE TRIGGER instance_settings_set_updated_at BEFORE UPDATE ON catwire.instance_settings
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();

CREATE TABLE IF NOT EXISTS catwire.chats (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id uuid NOT NULL REFERENCES catwire.instances (id) ON DELETE CASCADE,
  jid text NOT NULL,
  lid text,
  phone text,
  type text NOT NULL CHECK (type IN ('direct','group')),
  name text,
  last_message_id uuid,
  last_message_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chats_instance_jid_uq UNIQUE (instance_id, jid)
);

CREATE UNIQUE INDEX IF NOT EXISTS chats_instance_lid_uq
  ON catwire.chats (instance_id, lid)
  WHERE lid IS NOT NULL;
CREATE INDEX IF NOT EXISTS chats_instance_last_message_idx
  ON catwire.chats (instance_id, last_message_at DESC NULLS LAST, id DESC);

DROP TRIGGER IF EXISTS chats_set_updated_at ON catwire.chats;
CREATE TRIGGER chats_set_updated_at BEFORE UPDATE ON catwire.chats
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();

CREATE TABLE IF NOT EXISTS catwire.messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id uuid NOT NULL REFERENCES catwire.instances (id) ON DELETE CASCADE,
  chat_id uuid NOT NULL REFERENCES catwire.chats (id) ON DELETE CASCADE,
  wa_message_id text NOT NULL,
  direction text NOT NULL CHECK (direction IN ('inbound','outbound')),
  origin text NOT NULL CHECK (origin IN ('contact','api','device')),
  source text NOT NULL DEFAULT 'live' CHECK (source IN ('live','history')),
  sender_jid text NOT NULL,
  sender_phone text,
  sender_lid text,
  sender_push_name text,
  type text NOT NULL
    CHECK (type IN ('text','image','audio','video','document','sticker','location','contact','undecryptable','unsupported')),
  text text,
  content jsonb NOT NULL DEFAULT '{}'::jsonb,
  reply_to_wa_message_id text,
  is_forwarded boolean NOT NULL DEFAULT false,
  is_ephemeral boolean NOT NULL DEFAULT false,
  is_view_once boolean NOT NULL DEFAULT false,
  status text NOT NULL
    CHECK (status IN ('pending','sent','delivered','read','played','failed','received')),
  error_code text,
  error_message text,
  reactions jsonb NOT NULL DEFAULT '[]'::jsonb,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  idempotency_key text,
  request_hash text,
  send_attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz,
  wa_timestamp timestamptz NOT NULL,
  sent_at timestamptz,
  delivered_at timestamptz,
  read_at timestamptz,
  played_at timestamptz,
  edited_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT messages_instance_chat_wa_uq UNIQUE (instance_id, chat_id, wa_message_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS messages_instance_idempotency_uq
  ON catwire.messages (instance_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS messages_chat_timestamp_idx
  ON catwire.messages (chat_id, wa_timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS messages_instance_timestamp_idx
  ON catwire.messages (instance_id, wa_timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS messages_instance_wa_id_idx
  ON catwire.messages (instance_id, wa_message_id);
CREATE INDEX IF NOT EXISTS messages_send_queue_idx
  ON catwire.messages (instance_id, created_at, id)
  WHERE status = 'pending';

DROP TRIGGER IF EXISTS messages_set_updated_at ON catwire.messages;
CREATE TRIGGER messages_set_updated_at BEFORE UPDATE ON catwire.messages
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();

CREATE TABLE IF NOT EXISTS catwire.media (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id uuid NOT NULL REFERENCES catwire.instances (id) ON DELETE CASCADE,
  message_id uuid REFERENCES catwire.messages (id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('image','audio','video','document','sticker')),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','stored','failed','skipped')),
  mime_type text,
  file_name text,
  size_bytes bigint,
  sha256 text,
  width integer,
  height integer,
  duration_seconds integer,
  is_voice_note boolean NOT NULL DEFAULT false,
  storage_backend text,
  storage_key text,
  wa_direct_path text,
  wa_media_key bytea,
  wa_file_sha256 bytea,
  wa_file_enc_sha256 bytea,
  download_attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz,
  last_error text,
  stored_at timestamptz,
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS media_message_uq
  ON catwire.media (message_id)
  WHERE message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS media_download_queue_idx
  ON catwire.media (next_attempt_at, id)
  WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS media_unattached_expiry_idx
  ON catwire.media (expires_at)
  WHERE message_id IS NULL;

DROP TRIGGER IF EXISTS media_set_updated_at ON catwire.media;
CREATE TRIGGER media_set_updated_at BEFORE UPDATE ON catwire.media
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();

CREATE TABLE IF NOT EXISTS catwire.outbox_events (
  seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id uuid NOT NULL,
  event_type text NOT NULL,
  event_version smallint NOT NULL DEFAULT 1,
  client_id text NOT NULL,
  instance_id uuid,
  partition_key text NOT NULL,
  payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  dispatched_at timestamptz,
  kafka_published_at timestamptz,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT outbox_events_event_id_uq UNIQUE (event_id)
);

CREATE INDEX IF NOT EXISTS outbox_events_pending_idx
  ON catwire.outbox_events (seq)
  WHERE dispatched_at IS NULL;
CREATE INDEX IF NOT EXISTS outbox_events_dispatched_idx
  ON catwire.outbox_events (dispatched_at)
  WHERE dispatched_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS catwire.webhooks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  client_id text NOT NULL,
  url text NOT NULL,
  description text,
  secret text NOT NULL,
  events text[] NOT NULL DEFAULT '{}',
  instance_ids uuid[] NOT NULL DEFAULT '{}',
  active boolean NOT NULL DEFAULT true,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS webhooks_client_active_idx
  ON catwire.webhooks (client_id)
  WHERE active AND deleted_at IS NULL;

DROP TRIGGER IF EXISTS webhooks_set_updated_at ON catwire.webhooks;
CREATE TRIGGER webhooks_set_updated_at BEFORE UPDATE ON catwire.webhooks
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();

CREATE TABLE IF NOT EXISTS catwire.webhook_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  webhook_id uuid NOT NULL REFERENCES catwire.webhooks (id) ON DELETE CASCADE,
  event_id uuid NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','failed','succeeded','dead')),
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_status_code integer,
  last_error text,
  last_attempt_at timestamptz,
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT webhook_deliveries_webhook_event_uq UNIQUE (webhook_id, event_id)
);

CREATE INDEX IF NOT EXISTS webhook_deliveries_due_idx
  ON catwire.webhook_deliveries (next_attempt_at, id)
  WHERE status IN ('pending','failed');
CREATE INDEX IF NOT EXISTS webhook_deliveries_webhook_created_idx
  ON catwire.webhook_deliveries (webhook_id, created_at DESC, id DESC);

DROP TRIGGER IF EXISTS webhook_deliveries_set_updated_at ON catwire.webhook_deliveries;
CREATE TRIGGER webhook_deliveries_set_updated_at BEFORE UPDATE ON catwire.webhook_deliveries
  FOR EACH ROW EXECUTE FUNCTION catwire.set_updated_at();
