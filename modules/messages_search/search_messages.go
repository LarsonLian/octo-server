package messages_search

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/olivere/elastic"
	"go.uber.org/zap"
)

// MessageHit is the response shape per A doc §2.1.
type MessageHit struct {
	MessageID       string         `json:"message_id"`
	MessageSeq      int64          `json:"message_seq"`
	MessageKind     string         `json:"message_kind"`
	Snippet         string         `json:"snippet,omitempty"`
	SenderID        string         `json:"sender_id"`
	SenderName      string         `json:"sender_name,omitempty"`
	SenderAvatarURL string         `json:"sender_avatar_url,omitempty"`
	SentAt          string         `json:"sent_at"`
	OuterPreview    *OuterPreview  `json:"outer_preview,omitempty"`
	InnerMessages   []InnerMessage `json:"inner_messages,omitempty"`
	ChannelID       string         `json:"channel_id"`
}

func init() {
	registerRoute(func(h *Handler, g *wkhttp.RouterGroup) {
		g.POST("/_search", h.searchMessages)
	})
}

// searchMessages is POST /v1/messages/_search.
func (h *Handler) searchMessages(c *wkhttp.Context) {
	var req SearchMessagesReq
	if err := c.BindJSON(&req); err != nil {
		respondValidation(c, "body", "invalid JSON")
		return
	}
	req.Keyword = strings.TrimSpace(req.Keyword)
	loginUID := c.GetLoginUID()

	if !validateKeywordOptional(c, req.Keyword) {
		return
	}
	if !validateSearchNotEmpty(c, req.Keyword, req.Filters) {
		return
	}
	pageSize, ok := validateBase(c, h.cfg, req.ChannelType, req.ChannelID, req.Sort, req.Cursor, req.Filters, req.PageSize, req.Keyword != "")
	if !ok {
		return
	}
	if !h.checkChannelAccess(c, req.ChannelType, req.ChannelID, loginUID) {
		return
	}
	spaceID, ok := h.resolveP2PSpaceScope(c, req.ChannelType, loginUID)
	if !ok {
		return
	}

	client, err := ESClient(h.cfg)
	if err != nil {
		h.Error("ESClient init failed", zap.Error(err))
		respondUpstream(c)
		return
	}

	normID := normalizedChannelID(req.ChannelType, req.ChannelID, loginUID)
	isRelevance := req.Sort == "relevance"

	initialAfter, ok := decodeCursorAsSearchAfter(h.cfg, req.Cursor, isRelevance)
	if !ok {
		respondValidation(c, "cursor", "malformed")
		return
	}
	priorDepth, ok := h.resolveCursorDepth(c, req.Cursor, pageSize)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.cfg.Timeout)
	defer cancel()

	dsl, analyzeErr := buildSearchMessagesDSL(ctx, newOSIKSmartAnalyzer(client), h.cfg.StopwordStripEnabled, req, normID, spaceID)
	if analyzeErr != nil {
		h.Warn("messages_search: _analyze fallback (degraded keyword clause)", zap.Error(analyzeErr))
	}

	osQuery := func(searchAfter []any, size int) ([]*elastic.SearchHit, error) {
		svc := client.Search().
			Index(h.cfg.OSReadAlias).
			Routing(normID).
			Query(dsl).
			Size(size).
			TrackTotalHits(false)
		if req.Keyword != "" {
			svc = svc.Highlight(buildSearchMessagesHighlight())
		}
		svc = applySort(svc, req.Sort)
		if len(searchAfter) > 0 {
			svc = svc.SearchAfter(searchAfter...)
		}
		res, qerr := svc.Do(ctx)
		if qerr != nil {
			return nil, qerr
		}
		if res == nil || res.Hits == nil {
			return nil, nil
		}
		return res.Hits.Hits, nil
	}

	filtered, hasMore, nextCursor, err := h.paginateWithFilterDepth(
		ctx, loginUID, req.ChannelID, pageSize, priorDepth, initialAfter, isRelevance, osQuery, projectDocRef(req.ChannelID),
	)
	if err != nil {
		if responder := classifyOSError(err); responder != nil {
			h.Warn("OS search failed", zap.Error(err))
			responder(c)
			return
		}
		// filterVisible failures fall through to here; fail-closed with INTERNAL.
		h.Error("messages_search: visibility filter failed", zap.Error(err))
		respondInternal(c)
		return
	}

	items := h.buildMessageHits(ctx, filtered, req, loginUID)

	recordAudit(c, "search_messages", req.ChannelType, req.ChannelID, req.Keyword, len(items))
	c.Response(envelope(items, hasMore, nextCursor))
}

// buildSearchMessagesDSL constructs the bool query for /_search. Returns the
// query plus a non-nil error iff the keyword path's `_analyze` call failed and
// the keyword clause degraded to the fallback shape (raw keyword + MSM 75%);
// the query is still safe to use, callers should warn-log the error.
//
// stopwordStripEnabled = false routes through the ops kill switch: skip the
// `_analyze` call and emit the §4.4 degraded shape unconditionally.
func buildSearchMessagesDSL(ctx context.Context, analyzer tokenAnalyzer, stopwordStripEnabled bool, req SearchMessagesReq, normChannelID, spaceID string) (elastic.Query, error) {
	b := elastic.NewBoolQuery()
	var analyzeErr error
	if req.Keyword != "" {
		clause, err := buildKeywordClauseGated(ctx, analyzer, stopwordStripEnabled, req.Keyword,
			"payload.text.content^3",
			"payload.image.caption", "payload.image.name",
			"payload.file.caption", "payload.file.name",
			"payload.mergeForward.msgs.searchText",
		)
		b.Must(clause)
		analyzeErr = err
	}
	applyChannelAndRevoked(b, normChannelID)
	applySpaceIDScope(b, req.ChannelType, spaceID)
	addCommonFilters(b, req.Filters)
	applySystemMessageHardFilter(b)
	return b, analyzeErr
}

// buildSearchMessagesHighlight returns the standard highlight config for
// /_search responses. Each match returns at most one 120-char fragment.
func buildSearchMessagesHighlight() *elastic.Highlight {
	return elastic.NewHighlight().
		PreTags("<mark>").PostTags("</mark>").
		FragmentSize(120).
		NumOfFragments(1).
		Field("payload.text.content").
		Field("payload.mergeForward.msgs.searchText").
		Field("payload.image.caption").
		Field("payload.file.name")
}

// buildMessageHits maps the OS hits into the API response shape and joins
// sender display name + avatar in a single batch. The batch covers both the
// outer message sender and any inner_messages[].sender_id (forward children),
// so a single page incurs at most one MySQL round-trip for names regardless of
// how many forward cards it contains.
func (h *Handler) buildMessageHits(ctx context.Context, hits []*elastic.SearchHit, req SearchMessagesReq, loginUID string) []MessageHit {
	if len(hits) == 0 {
		return []MessageHit{}
	}
	items := make([]MessageHit, 0, len(hits))
	senderIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		var doc Doc
		if err := json.Unmarshal(rawSource(hit.Source), &doc); err != nil {
			h.Warn("messages_search: bad _source skipped", zap.Error(err))
			continue
		}
		hl := map[string][]string(hit.Highlight)
		mh := h.singleMessageHit(doc, req.ChannelID, hl)
		senderIDs = append(senderIDs, mh.SenderID)
		for _, im := range mh.InnerMessages {
			if im.SenderID != "" {
				senderIDs = append(senderIDs, im.SenderID)
			}
		}
		items = append(items, mh)
	}

	if len(items) == 0 {
		return items
	}
	join := h.senderJoin(ctx, uniqUIDs(senderIDs), req.ChannelType, req.ChannelID)
	for i := range items {
		items[i].SenderName = join.Names[items[i].SenderID]
		items[i].SenderAvatarURL = join.Avatars[items[i].SenderID]
		for j := range items[i].InnerMessages {
			if uid := items[i].InnerMessages[j].SenderID; uid != "" {
				items[i].InnerMessages[j].SenderName = join.Names[uid]
			}
		}
	}
	return items
}

// singleMessageHit projects a single Doc into a MessageHit. Extracted so unit
// tests can drive the field mapping (kind / snippet / outer_preview) without
// standing up a full search loop, and so search_all can reuse it.
func (h *Handler) singleMessageHit(doc Doc, reqChannelID string, hl map[string][]string) MessageHit {
	// Prefer the keyword highlight fragment; on the empty-keyword browse path
	// no highlight is requested, so fall back to the raw payload text so the
	// hit still carries readable content (A-doc §2.1).
	snippet := pickSnippet(hl)
	if snippet == "" {
		snippet = fallbackSnippet(doc.Payload)
	}
	return MessageHit{
		MessageID:     strconv.FormatInt(doc.MessageID, 10),
		MessageSeq:    int64(doc.MessageSeq),
		MessageKind:   classifyKind(doc.Payload),
		Snippet:       snippet,
		SenderID:      doc.From,
		SentAt:        msToRFC3339(doc.Timestamp),
		OuterPreview:  buildOuterPreview(doc.Payload),
		InnerMessages: buildInnerMessages(doc.Payload),
		ChannelID:     encodeChannelID(reqChannelID),
	}
}
