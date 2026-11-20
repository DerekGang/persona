package proto

import (
	"crypto/sha256"
	"encoding/hex"
)

// ApprovalMirrorIDPrefix is reserved for display copies. An authoritative
// interaction with this prefix is rejected at manager and hub ingestion.
const ApprovalMirrorIDPrefix = "assistant-main-mirror-"

// ApprovalMirrorID is the only display ID the hub accepts for an authoritative
// interaction's main-conversation copy. A runtime-chosen ID cannot alias an
// unrelated pending operation into that interaction's decision path.
func ApprovalMirrorID(id string) string {
	sum := sha256.Sum256([]byte("assistant-main-approval-mirror\x00" + id))
	return ApprovalMirrorIDPrefix + hex.EncodeToString(sum[:])
}
