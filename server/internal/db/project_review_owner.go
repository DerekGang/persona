package db

import (
	"context"
	"time"
)

type ProjectReviewOwnerRelay struct {
	MessageID, Manager, Machine, Session string
	SendOutcome                          string
	At                                   time.Time
	Unscoped                             bool
}

// SettleProjectReviewOwner records successful transport and the pending manager
// notice in one commit. A lost manager acknowledgment leaves the notice queued.
func (d *DB) SettleProjectReviewOwner(ctx context.Context, p ProjectReviewOwnerRelay) error {
	_, err := d.pool.Exec(ctx, `WITH settled AS (
		UPDATE sent_messages SET state=$5 WHERE client_msg_id=$1 RETURNING client_msg_id
	) INSERT INTO project_review_owner_relay(message_id,manager,machine,session,unscoped)
	SELECT client_msg_id,$2,$3,$4,$6 FROM settled WHERE EXISTS (
	    SELECT FROM project_review_owner_managers WHERE manager=$2
	) ON CONFLICT DO NOTHING`, p.MessageID, p.Manager, p.Machine, p.Session, p.SendOutcome, p.Unscoped)
	return err
}

func (d *DB) PendingProjectReviewOwners(ctx context.Context) ([]ProjectReviewOwnerRelay, error) {
	rows, err := d.pool.Query(ctx, `SELECT message_id,manager,machine,session,queued_at,unscoped FROM project_review_owner_relay
		ORDER BY manager,queued_at,message_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectReviewOwnerRelay
	for rows.Next() {
		var p ProjectReviewOwnerRelay
		if err := rows.Scan(&p.MessageID, &p.Manager, &p.Machine, &p.Session, &p.At, &p.Unscoped); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) AckProjectReviewOwner(ctx context.Context, id string) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM project_review_owner_relay WHERE message_id=$1`, id)
	return err
}

// SetProjectReviewAppInputs keeps the owner-approved App source switch across manager disconnects.
func (d *DB) SetProjectReviewAppInputs(ctx context.Context, manager string, enabled bool) error {
	if enabled {
		_, err := d.pool.Exec(ctx, `INSERT INTO project_review_owner_managers VALUES ($1) ON CONFLICT DO NOTHING`, manager)
		return err
	}
	_, err := d.pool.Exec(ctx, `DELETE FROM project_review_owner_managers WHERE manager=$1`, manager)
	return err
}

// RecordProjectReviewOwnerInput queues an authenticated prompt when the native operation has its own receipt.
func (d *DB) RecordProjectReviewOwnerInput(ctx context.Context, p ProjectReviewOwnerRelay) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO project_review_owner_relay(message_id,manager,machine,session,unscoped)
		SELECT $1,$2,$3,$4,$5 WHERE EXISTS (SELECT FROM project_review_owner_managers WHERE manager=$2)
		ON CONFLICT DO NOTHING`, p.MessageID, p.Manager, p.Machine, p.Session, p.Unscoped)
	return err
}
