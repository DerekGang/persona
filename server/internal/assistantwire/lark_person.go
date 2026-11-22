package assistantwire

import (
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
)

// In-process manager calls for a generic Feishu bot conversation. These are not HTTP routes and carry no message body.
const (
	MethodLarkPersonPark   = "lark_person.park"
	MethodLarkPersonState  = "lark_person.state"
	MethodLarkPersonClose  = "lark_person.close"
	MethodLarkPersonRecord = "lark_person.record_reply"
)

type LarkPersonPark struct {
	TaskID       string                       `json:"task_id"`
	ParkInstance string                       `json:"park_instance"`
	Title        string                       `json:"title"`
	DisplayName  string                       `json:"display_name"`
	Watch        connectorwire.LarkReplyWatch `json:"watch"`
	URL          string                       `json:"url"`
	SentAt       time.Time                    `json:"sent_at"`
}

// LarkPersonTask exposes only generic lifecycle/review states to the Bots source, never assistant instructions or IDs.
type LarkPersonTask struct {
	Lifecycle   string `json:"lifecycle"`
	ReviewState string `json:"review_state,omitempty"`
}

type LarkPersonClose struct {
	TaskID string `json:"task_id"`
	Action string `json:"action"` // complete | abandon
}

type LarkPersonReplyRecord struct {
	TaskID       string                             `json:"task_id"`
	ParkInstance string                             `json:"park_instance"`
	Observation  connectorwire.LarkReplyObservation `json:"observation"`
}
