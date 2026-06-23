package messages_search

import "strconv"

// Doc mirrors the OpenSearch `_source` shape produced by
// wukongim-message-indexer (see indexer-os-changes.md §3.2). We only
// deserialise structured `payload.*` subobjects — `payloadRaw` is
// `enabled:false` in the mapping and would force per-doc JSON parsing on the
// hot path with no upside.
type Doc struct {
	MessageID   int64    `json:"messageId"`
	MessageSeq  uint64   `json:"messageSeq"`
	From        string   `json:"from,omitempty"`
	To          string   `json:"to,omitempty"`
	ChannelID   string   `json:"channelId"`
	ChannelType uint32   `json:"channelType"`
	Timestamp   int64    `json:"timestamp"`
	Payload     *Payload `json:"payload,omitempty"`
	Revoked     bool     `json:"revoked,omitempty"`
	// SpaceID mirrors the OS doc's `spaceId` keyword introduced in v1.9 to
	// scope DM (p2p) search by Space membership. The indexer derives this
	// from `payload.space_id`; older documents without the field are
	// fail-closed by the term filter in applySpaceIDScope (no match → no
	// hit) rather than implicitly visible.
	SpaceID string `json:"spaceId,omitempty"`
	// Visibles is the per-message allowlist a sender may attach to a group
	// message so only the listed UIDs see it (mirrors the read-path gate
	// in modules/message/api.go::MsgSyncResp.from at the visibles-array
	// branch). When non-empty and the caller's UID is absent the search
	// post-filter must drop the hit. Schema is reserved here ahead of the
	// indexer write — see CONSTRAINTS-2026-06-12 for the transient
	// fail-open while the field is unwritten.
	Visibles []string `json:"visibles,omitempty"`
}

// Payload is the structured projection of the message payload. Each typed
// subobject is allocated only when the indexer recognised its content type, so
// a non-nil pointer is the strongest "this message is of type X" signal.
type Payload struct {
	Type         *int                 `json:"type,omitempty"`
	Text         *TextPayload         `json:"text,omitempty"`
	Image        *ImagePayload        `json:"image,omitempty"`
	Gif          *GifPayload          `json:"gif,omitempty"`
	Voice        *VoicePayload        `json:"voice,omitempty"`
	Video        *VideoPayload        `json:"video,omitempty"`
	File         *FilePayload         `json:"file,omitempty"`
	MergeForward *MergeForwardPayload `json:"mergeForward,omitempty"`
}

type TextPayload struct {
	Content string `json:"content,omitempty"`
}

type ImagePayload struct {
	URL     string `json:"url,omitempty"`
	Caption string `json:"caption,omitempty"`
	Name    string `json:"name,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
}

type GifPayload struct {
	URL string `json:"url,omitempty"`
}

type VoicePayload struct {
	URL string `json:"url,omitempty"`
}

type VideoPayload struct {
	URL    string `json:"url,omitempty"`
	Cover  string `json:"cover,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Second int    `json:"second,omitempty"`
}

type FilePayload struct {
	URL       string `json:"url,omitempty"`
	Name      string `json:"name,omitempty"`
	Caption   string `json:"caption,omitempty"`
	SizeBytes int64  `json:"size,omitempty"`
	Ext       string `json:"extension,omitempty"`
}

type MergeForwardPayload struct {
	ChildCount int               `json:"childCount,omitempty"`
	Msgs       []MergeForwardMsg `json:"msgs,omitempty"`
}

// MergeForwardMsg is the per-child projection from `payload.mergeForward.msgs[]`.
// `from` and `timestamp` are forward-compat fields the indexer will start
// writing in a follow-up release; both are omitempty so older OS docs (which
// only carry messageId/type/searchText) deserialise to a zero value and the
// API can degrade `sender_id` / `sent_at` to omitted on the wire.
type MergeForwardMsg struct {
	MessageID  int64  `json:"messageId"`
	Type       int    `json:"type"`
	SearchText string `json:"searchText,omitempty"`
	From       string `json:"from,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
}

// Payload type IDs (mirroring dmwork-lib `common/msg.go::ContentType`). Kept
// as untyped int constants because OS stores them as `int` and our DSL/filter
// layer needs the raw value.
const (
	payloadTypeText         = 1
	payloadTypeImage        = 2
	payloadTypeGIF          = 3
	payloadTypeVoice        = 4
	payloadTypeVideo        = 5
	payloadTypeFile         = 8
	payloadTypeMergeForward = 11
	payloadTypeCmd          = 99
)

// classifyKind decides the response `message_kind` for /v1/messages/_search.
// Per A doc v4.2 we only emit "forward" or "text"; quote/reply messages get
// folded into "text" because the outer payload.text is what matched.
func classifyKind(p *Payload) string {
	if p != nil && p.MergeForward != nil {
		return "forward"
	}
	return "text"
}

// OuterPreview is the optional summary card returned for forward messages.
type OuterPreview struct {
	ChildCount int `json:"child_count"`
}

// buildOuterPreview emits a non-nil preview only when the doc is a forward
// card with a positive child_count. Plain text and quote messages return nil;
// forwards with a missing or non-positive childCount also return nil so we
// don't surface the misleading `{child_count: 0}` to the client.
func buildOuterPreview(p *Payload) *OuterPreview {
	if p == nil || p.MergeForward == nil {
		return nil
	}
	if p.MergeForward.ChildCount <= 0 {
		return nil
	}
	return &OuterPreview{ChildCount: p.MergeForward.ChildCount}
}

// payloadType returns the typed payload.type or 0 when missing. Used by the
// _search_all dispatcher to pick a result_type without dereferencing a *int
// at every call site.
func payloadType(p *Payload) int {
	if p == nil || p.Type == nil {
		return 0
	}
	return *p.Type
}

// InnerMessage is the per-child shape surfaced under MessageHit.inner_messages
// for forward (type=11) hits. SenderName is filled in after senderJoin runs;
// SenderID / SentAt are omitted when the indexer hasn't yet populated the
// underlying msgs[].from / msgs[].timestamp fields.
type InnerMessage struct {
	MessageID  string `json:"message_id"`
	Type       int    `json:"type"`
	SearchText string `json:"search_text,omitempty"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	SentAt     string `json:"sent_at,omitempty"`
}

// buildInnerMessages projects the forward card's child messages onto the API
// shape. Returns nil for non-forward payloads or empty msgs[] so the response
// field is omitted entirely (omitempty) rather than emitting `[]`.
//
// `sender_name` is left empty here — the caller batches all child uids into
// the page's senderJoin and fills the names afterwards.
func buildInnerMessages(p *Payload) []InnerMessage {
	if p == nil || p.MergeForward == nil {
		return nil
	}
	if len(p.MergeForward.Msgs) == 0 {
		return nil
	}
	out := make([]InnerMessage, 0, len(p.MergeForward.Msgs))
	for _, m := range p.MergeForward.Msgs {
		im := InnerMessage{
			MessageID:  strconv.FormatInt(m.MessageID, 10),
			Type:       m.Type,
			SearchText: m.SearchText,
			SenderID:   m.From,
		}
		if m.Timestamp > 0 {
			im.SentAt = msToRFC3339(m.Timestamp)
		}
		out = append(out, im)
	}
	return out
}
