package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nwverse/persona/server/internal/conversationlog"
)

var (
	ErrConversationEpoch      = errors.New("conversation activation changed")
	ErrConversationSwitchBusy = errors.New("conversation switch is unfinished")
)

type ConversationSwitch struct {
	OperationID   string          `json:"operation_id"`
	ExpectedEpoch int64           `json:"expected_epoch"`
	SourceSegment string          `json:"source_segment"`
	TargetSegment string          `json:"target_segment,omitempty"`
	Request       json.RawMessage `json:"request"`
	Phase         string          `json:"phase"`
	Error         string          `json:"error,omitempty"`
}

type LogicalConversation struct {
	MachineID         string                    `json:"machine_id"`
	SessionID         string                    `json:"session_id"`
	Epoch             int64                     `json:"epoch"`
	History           string                    `json:"history"`
	ProjectionVersion int64                     `json:"projection_version"`
	Revision          int64                     `json:"revision"`
	Deleted           bool                      `json:"deleted"`
	Segments          []conversationlog.Segment `json:"segments"`
	Switch            *ConversationSwitch       `json:"switch,omitempty"`
}

func (c LogicalConversation) Active() (conversationlog.Segment, bool) {
	for _, s := range c.Segments {
		if s.State == "active" {
			return s, true
		}
	}
	return conversationlog.Segment{}, false
}

type conversationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// A physical alias is resolved only inside the manager. It grants no caller authority.
func readLogicalConversation(ctx context.Context, q conversationQuerier, machine, session string) (*LogicalConversation, error) {
	c := &LogicalConversation{}
	err := q.QueryRow(ctx, `SELECT c.machine_id,c.session_id,c.epoch,c.history,c.projection_version,c.revision,c.deleted_at IS NOT NULL
	 FROM logical_conversations c WHERE (c.machine_id=$1 AND c.session_id=$2) OR EXISTS
	 (SELECT 1 FROM conversation_segments s WHERE s.logical_machine=c.machine_id AND s.logical_session=c.session_id AND s.machine_id=$1 AND s.session_id=$2)`, machine, session).
		Scan(&c.MachineID, &c.SessionID, &c.Epoch, &c.History, &c.ProjectionVersion, &c.Revision, &c.Deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT id,ordinal,machine_id,session_id,engine,source_history,source_revision,seq_offset,frozen_end,state,close_pending
	 FROM conversation_segments WHERE logical_machine=$1 AND logical_session=$2 ORDER BY ordinal`, c.MachineID, c.SessionID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s conversationlog.Segment
		if err = rows.Scan(&s.ID, &s.Ordinal, &s.MachineID, &s.SessionID, &s.Engine, &s.SourceHistory, &s.SourceRevision, &s.Offset, &s.FrozenEnd, &s.State, &s.ClosePending); err != nil {
			rows.Close()
			return nil, err
		}
		c.Segments = append(c.Segments, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var op ConversationSwitch
	var request []byte
	err = q.QueryRow(ctx, `SELECT operation_id,expected_epoch,source_segment,coalesce(target_segment,''),request,phase,error
	 FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND phase IN ('preparing','prepared','active','in_doubt')`, c.MachineID, c.SessionID).
		Scan(&op.OperationID, &op.ExpectedEpoch, &op.SourceSegment, &op.TargetSegment, &request, &op.Phase, &op.Error)
	if err == nil {
		op.Request = json.RawMessage(request)
		c.Switch = &op
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return c, nil
}

func (d *DB) LogicalConversation(ctx context.Context, machine, session string) (*LogicalConversation, error) {
	return readLogicalConversation(ctx, d.pool, machine, session)
}

func (d *DB) LogicalConversations(ctx context.Context) ([]LogicalConversation, error) {
	rows, err := d.pool.Query(ctx, `SELECT machine_id,session_id FROM logical_conversations WHERE deleted_at IS NULL ORDER BY machine_id,session_id`)
	if err != nil {
		return nil, err
	}
	var keys [][2]string
	for rows.Next() {
		var k [2]string
		if err = rows.Scan(&k[0], &k[1]); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []LogicalConversation
	for _, k := range keys {
		c, e := d.LogicalConversation(ctx, k[0], k[1])
		if e != nil {
			return nil, e
		}
		if c != nil {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (d *DB) AdoptConversation(ctx context.Context, machine, session, segment, engine, history string) error {
	if machine == "" || session == "" || segment == "" || engine == "" || history == "" {
		return errors.New("incomplete conversation identity")
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := conversationAvailableTx(ctx, tx, machine, session); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO logical_conversations(machine_id,session_id,history,revision) VALUES($1,$2,$3,coalesce((SELECT revision FROM conversation_histories WHERE machine_id=$1 AND session_id=$2),0)) ON CONFLICT DO NOTHING`, machine, session, history)
		if err != nil {
			return err
		}
		var deleted bool
		if err = tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 FOR UPDATE`, machine, session).Scan(&deleted); err != nil {
			return err
		}
		if deleted {
			return ErrConversationDeleted
		}
		_, err = tx.Exec(ctx, `INSERT INTO conversation_segments(id,logical_machine,logical_session,ordinal,machine_id,session_id,engine,source_history,seq_offset,state)
		 VALUES($1,$2,$3,1,$2,$3,$4,$5,0,'active') ON CONFLICT(logical_machine,logical_session,ordinal) DO NOTHING`, segment, machine, session, engine, history)
		return err
	})
}

// The shared database lock fences activation/deletion across overlapping managers,
// including the actual bounded send or tool action, not just a cached lookup.
func (d *DB) WithConversationEpoch(ctx context.Context, machine, session string, epoch int64, fn func(*LogicalConversation) error) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var current int64
		var deleted bool
		err := tx.QueryRow(ctx, `SELECT epoch,deleted_at IS NOT NULL FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 FOR SHARE`, machine, session).Scan(&current, &deleted)
		if err != nil {
			return err
		}
		if deleted {
			return ErrConversationDeleted
		}
		if current != epoch {
			return ErrConversationEpoch
		}
		c, err := readLogicalConversation(ctx, tx, machine, session)
		if err != nil {
			return err
		}
		return fn(c)
	})
}

func (d *DB) BeginConversationSwitch(ctx context.Context, machine, session string, op ConversationSwitch) (result error) {
	defer func() {
		var pe *pgconn.PgError
		if errors.As(result, &pe) && pe.Code == "55P03" {
			result = ErrConversationSwitchBusy
		}
	}()
	if op.OperationID == "" || len(op.OperationID) > 128 || !json.Valid(op.Request) || len(op.Request) > 65536 {
		return errors.New("invalid switch operation")
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var epoch int64
		var deleted bool
		if err := tx.QueryRow(ctx, `SELECT epoch,deleted_at IS NOT NULL FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 FOR UPDATE NOWAIT`, machine, session).Scan(&epoch, &deleted); err != nil {
			return err
		}
		if deleted {
			return ErrConversationDeleted
		}
		var old []byte
		var expected int64
		err := tx.QueryRow(ctx, `SELECT request,expected_epoch FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, op.OperationID).Scan(&old, &expected)
		if err == nil {
			if expected != op.ExpectedEpoch || !bytes.Equal(old, op.Request) {
				return ErrConversationEpoch
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if epoch != op.ExpectedEpoch {
			return ErrConversationEpoch
		}
		var unfinished bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND phase IN ('preparing','prepared','active','in_doubt'))`, machine, session).Scan(&unfinished); err != nil {
			return err
		}
		if unfinished {
			return ErrConversationSwitchBusy
		}
		var active string
		if err = tx.QueryRow(ctx, `SELECT id FROM conversation_segments WHERE logical_machine=$1 AND logical_session=$2 AND state='active'`, machine, session).Scan(&active); err != nil {
			return err
		}
		if active != op.SourceSegment {
			return ErrConversationEpoch
		}
		_, err = tx.Exec(ctx, `INSERT INTO conversation_switches(logical_machine,logical_session,operation_id,expected_epoch,source_segment,request,phase) VALUES($1,$2,$3,$4,$5,$6,'preparing')`, machine, session, op.OperationID, op.ExpectedEpoch, active, []byte(op.Request))
		return err
	})
}

func (d *DB) PrepareConversationSegment(ctx context.Context, machine, session, operation string, target conversationlog.Segment, frozen int) error {
	if _, err := target.Seq(0); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var epoch int64
		if err := tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 AND deleted_at IS NULL FOR UPDATE`, machine, session).Scan(&epoch); err != nil {
			return err
		}
		var source, phase string
		var expected int64
		if err := tx.QueryRow(ctx, `SELECT source_segment,phase,expected_epoch FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation).Scan(&source, &phase, &expected); err != nil {
			return err
		}
		if expected != epoch || phase != "preparing" {
			return ErrConversationEpoch
		}
		var offset, ordinal int
		if err := tx.QueryRow(ctx, `SELECT seq_offset,ordinal FROM conversation_segments WHERE id=$1 AND state='active'`, source).Scan(&offset, &ordinal); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT max(ordinal) FROM conversation_segments WHERE logical_machine=$1 AND logical_session=$2`, machine, session).Scan(&ordinal); err != nil {
			return err
		}
		if frozen < 0 || offset >= conversationlog.MaxLogicalSeq-frozen {
			return conversationlog.ErrSequenceOverflow
		}
		_, err := tx.Exec(ctx, `UPDATE conversation_segments SET frozen_end=$2 WHERE id=$1`, source, frozen)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO conversation_segments(id,logical_machine,logical_session,ordinal,machine_id,session_id,engine,source_history,seq_offset,state)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'prepared')`, target.ID, machine, session, ordinal+1, target.MachineID, target.SessionID, target.Engine, target.SourceHistory, offset+frozen)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE conversation_switches SET target_segment=$4,phase='prepared',updated_at=now() WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation, target.ID)
		return err
	})
}

func (d *DB) ActivateConversationSegment(ctx context.Context, machine, session, operation string, expected int64) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var epoch int64
		if err := tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 AND deleted_at IS NULL FOR UPDATE`, machine, session).Scan(&epoch); err != nil {
			return err
		}
		if epoch != expected {
			return ErrConversationEpoch
		}
		var source, target, phase string
		var opEpoch int64
		if err := tx.QueryRow(ctx, `SELECT source_segment,target_segment,phase,expected_epoch FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation).Scan(&source, &target, &phase, &opEpoch); err != nil {
			return err
		}
		if phase != "prepared" || opEpoch != expected {
			return ErrConversationEpoch
		}
		if _, err := tx.Exec(ctx, `UPDATE conversation_segments SET state='retired' WHERE id=$1 AND state='active'`, source); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE conversation_segments SET state='active' WHERE id=$1 AND state='prepared'`, target)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConversationEpoch
		}
		tag, err = tx.Exec(ctx, `UPDATE logical_conversations SET epoch=epoch+1,revision=revision+1 WHERE machine_id=$1 AND session_id=$2 AND epoch=$3 AND deleted_at IS NULL`, machine, session, expected)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConversationEpoch
		}
		_, err = tx.Exec(ctx, `UPDATE conversation_switches SET phase='active',updated_at=now() WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation)
		return err
	})
}

// ResolveConversationSwitch is a forward-only outcome. The source is marked for
// closure only after acceptance; a definite rejection restores it at a new epoch.
func (d *DB) ResolveConversationSwitch(ctx context.Context, machine, session, operation, outcome, reason string) error {
	if outcome != "accepted" && outcome != "rejected" && outcome != "in_doubt" {
		return errors.New("invalid switch outcome")
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var epoch int64
		if err := tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 AND deleted_at IS NULL FOR UPDATE`, machine, session).Scan(&epoch); err != nil {
			return err
		}
		var source, target, phase string
		var expected int64
		if err := tx.QueryRow(ctx, `SELECT source_segment,coalesce(target_segment,''),phase,expected_epoch FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation).Scan(&source, &target, &phase, &expected); err != nil {
			return err
		}
		if phase == outcome {
			return nil
		}
		if phase == "accepted" || phase == "rejected" {
			return ErrConversationEpoch
		}
		activated := phase == "active" || epoch == expected+1
		if outcome == "accepted" {
			if !activated || epoch != expected+1 {
				return ErrConversationEpoch
			}
			if _, err := tx.Exec(ctx, `UPDATE conversation_segments SET close_pending=true WHERE id=$1 AND state='retired'`, source); err != nil {
				return err
			}
		}
		if outcome == "rejected" {
			if target != "" {
				if _, err := tx.Exec(ctx, `UPDATE conversation_segments SET state='abandoned',frozen_end=0,close_pending=true WHERE id=$1`, target); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE conversation_segments SET state='active',frozen_end=NULL,close_pending=false WHERE id=$1`, source); err != nil {
				return err
			}
			if activated {
				if epoch != expected+1 {
					return ErrConversationEpoch
				}
				if _, err := tx.Exec(ctx, `UPDATE logical_conversations SET epoch=epoch+1,revision=revision+1 WHERE machine_id=$1 AND session_id=$2 AND epoch=$3`, machine, session, epoch); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE conversation_switches SET phase=$4,error=$5,updated_at=now() WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation, outcome, reason)
		return err
	})
}

func (d *DB) ConversationSwitch(ctx context.Context, machine, session, operation string) (*ConversationSwitch, error) {
	var op ConversationSwitch
	var raw []byte
	err := d.pool.QueryRow(ctx, `SELECT operation_id,expected_epoch,source_segment,coalesce(target_segment,''),request,phase,error FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND operation_id=$3`, machine, session, operation).
		Scan(&op.OperationID, &op.ExpectedEpoch, &op.SourceSegment, &op.TargetSegment, &raw, &op.Phase, &op.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	op.Request = json.RawMessage(raw)
	return &op, err
}

func (d *DB) ObserveConversationSource(ctx context.Context, machine, session, history string) error {
	if history == "" {
		return errors.New("missing source generation")
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		c, err := readLogicalConversation(ctx, tx, machine, session)
		if err != nil || c == nil {
			return err
		}
		var epoch int64
		if err = tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 FOR UPDATE`, c.MachineID, c.SessionID).Scan(&epoch); err != nil {
			return err
		}
		var id, old, state string
		if err = tx.QueryRow(ctx, `SELECT id,source_history,state FROM conversation_segments WHERE machine_id=$1 AND session_id=$2 FOR UPDATE`, machine, session).Scan(&id, &old, &state); err != nil {
			return err
		}
		if old == history {
			return nil
		}
		if old != "" && state != "abandoned" && state != "prepared" {
			if _, err = tx.Exec(ctx, `UPDATE logical_conversations SET projection_version=projection_version+1,revision=revision+1,history='logical:'||md5(history||$3||$4||(projection_version+1)::text) WHERE machine_id=$1 AND session_id=$2`, c.MachineID, c.SessionID, id, history); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE conversation_segments SET source_history=$3 WHERE machine_id=$1 AND session_id=$2`, machine, session, history)
		return err
	})
}

func (d *DB) ConversationSegmentClosed(ctx context.Context, id string) error {
	_, err := d.pool.Exec(ctx, `UPDATE conversation_segments SET close_pending=false WHERE id=$1 AND state IN ('retired','abandoned')`, id)
	return err
}

func (d *DB) ObserveConversationRevision(ctx context.Context, machine, session string, revision int64) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		c, err := readLogicalConversation(ctx, tx, machine, session)
		if err != nil || c == nil {
			return err
		}
		var epoch int64
		if err = tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 FOR UPDATE`, c.MachineID, c.SessionID).Scan(&epoch); err != nil {
			return err
		}
		var old int64
		var state string
		if err = tx.QueryRow(ctx, `SELECT source_revision,state FROM conversation_segments WHERE machine_id=$1 AND session_id=$2`, machine, session).Scan(&old, &state); err != nil {
			return err
		}
		if old == revision {
			return nil
		}
		if state == "active" {
			if _, err = tx.Exec(ctx, `UPDATE logical_conversations SET revision=greatest(revision+1,$3) WHERE machine_id=$1 AND session_id=$2`, c.MachineID, c.SessionID, revision); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE conversation_segments SET source_revision=$3 WHERE machine_id=$1 AND session_id=$2`, machine, session, revision)
		return err
	})
}

type ConversationInput struct {
	ClientMsgID string
	Position    int64
	Epoch       int64
	Payload     json.RawMessage
	State       string
}

// Enqueue is idempotent by the original client message ID, not its text.
func (d *DB) EnqueueConversationInput(ctx context.Context, machine, session, id string, epoch int64, payload json.RawMessage) error {
	if id == "" || len(id) > 512 || len(payload) > 1048576 || !json.Valid(payload) {
		return errors.New("invalid conversation input")
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var current int64
		if err := tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 AND deleted_at IS NULL FOR SHARE`, machine, session).Scan(&current); err != nil {
			return err
		}
		if current != epoch {
			return ErrConversationEpoch
		}
		_, err := tx.Exec(ctx, `INSERT INTO conversation_inputs(logical_machine,logical_session,client_msg_id,epoch,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, machine, session, id, epoch, []byte(payload))
		if err != nil {
			return err
		}
		var old []byte
		if err = tx.QueryRow(ctx, `SELECT payload FROM conversation_inputs WHERE logical_machine=$1 AND logical_session=$2 AND client_msg_id=$3`, machine, session, id).Scan(&old); err != nil {
			return err
		}
		if !bytes.Equal(old, payload) {
			return errors.New("message identity reused for different input")
		}
		return nil
	})
}

// Claim exactly one queue head. A sending/in-doubt head fences later input until
// reconciled: process restarts cannot blindly repeat an ambiguous model turn.
func (d *DB) ClaimConversationInput(ctx context.Context, machine, session string, epoch int64) (*ConversationInput, error) {
	var out *ConversationInput
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var current int64
		if err := tx.QueryRow(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 AND deleted_at IS NULL FOR UPDATE`, machine, session).Scan(&current); err != nil {
			return err
		}
		if current != epoch {
			return ErrConversationEpoch
		}
		var phase string
		err := tx.QueryRow(ctx, `SELECT phase FROM conversation_switches WHERE logical_machine=$1 AND logical_session=$2 AND phase IN ('preparing','prepared','active','in_doubt')`, machine, session).Scan(&phase)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && phase != "active" {
			return nil
		}
		var row ConversationInput
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT client_msg_id,position,epoch,payload,state FROM conversation_inputs WHERE logical_machine=$1 AND logical_session=$2 AND state IN ('queued','sending','in_doubt') ORDER BY position LIMIT 1 FOR UPDATE`, machine, session).
			Scan(&row.ClientMsgID, &row.Position, &row.Epoch, &raw, &row.State)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.State != "queued" {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE conversation_inputs SET state='sending',epoch=$4 WHERE logical_machine=$1 AND logical_session=$2 AND client_msg_id=$3`, machine, session, row.ClientMsgID, epoch); err != nil {
			return err
		}
		row.Payload = json.RawMessage(raw)
		row.State = "sending"
		row.Epoch = epoch
		out = &row
		return nil
	})
	return out, err
}

func (d *DB) ResolveConversationInput(ctx context.Context, machine, session, id, state string) error {
	if state != "accepted" && state != "rejected" && state != "in_doubt" {
		return errors.New("invalid input outcome")
	}
	_, err := d.pool.Exec(ctx, `UPDATE conversation_inputs SET state=$4 WHERE logical_machine=$1 AND logical_session=$2 AND client_msg_id=$3 AND state IN ('sending','in_doubt')`, machine, session, id, state)
	return err
}

type ConversationInteractionRoute struct {
	MachineID, SessionID, PhysicalMachine, PhysicalSession string
	Epoch                                                  int64
}

func (d *DB) PutConversationInteraction(ctx context.Context, id, machine, session, physicalMachine, physicalSession string, epoch int64) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO conversation_interaction_routes(interaction_id,logical_machine,logical_session,machine_id,session_id,epoch)
	 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, id, machine, session, physicalMachine, physicalSession, epoch)
	if err != nil {
		return err
	}
	old, err := d.ConversationInteraction(ctx, id)
	if err != nil {
		return err
	}
	if old == nil || old.MachineID != machine || old.SessionID != session || old.PhysicalMachine != physicalMachine || old.PhysicalSession != physicalSession || old.Epoch != epoch {
		return ErrConversationEpoch
	}
	return nil
}

func (d *DB) ConversationInteraction(ctx context.Context, id string) (*ConversationInteractionRoute, error) {
	r := &ConversationInteractionRoute{}
	err := d.pool.QueryRow(ctx, `SELECT logical_machine,logical_session,machine_id,session_id,epoch FROM conversation_interaction_routes WHERE interaction_id=$1`, id).
		Scan(&r.MachineID, &r.SessionID, &r.PhysicalMachine, &r.PhysicalSession, &r.Epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}
