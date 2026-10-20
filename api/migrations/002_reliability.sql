ALTER TABLE connections ADD COLUMN next_send_at timestamptz;
CREATE TABLE message_mutations (
 connection_id uuid NOT NULL REFERENCES connections ON DELETE CASCADE,
 external_id text NOT NULL,
 edited_text text,
 edited_at timestamptz,
 deleted_at timestamptz,
 PRIMARY KEY(connection_id,external_id)
);
