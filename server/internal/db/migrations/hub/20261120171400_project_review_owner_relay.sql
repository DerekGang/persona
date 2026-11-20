CREATE TABLE project_review_owner_relay (
    message_id text PRIMARY KEY,
    manager text NOT NULL,
    machine text NOT NULL,
    session text NOT NULL,
    unscoped boolean NOT NULL,
    queued_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX project_review_owner_relay_order ON project_review_owner_relay (manager, queued_at, message_id);

CREATE TABLE project_review_owner_managers (
    manager text PRIMARY KEY
);
