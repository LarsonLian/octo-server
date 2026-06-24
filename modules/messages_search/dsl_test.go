package messages_search

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/olivere/elastic"
)

// fallbackTestAnalyzer triggers buildKeywordClause's fallback path (raw
// keyword + cross_fields + MSM 75%), which is what shape tests for the
// non-keyword DSL plumbing want — the original keyword stays intact in the
// emitted query, so the historical substring assertions continue to apply.
// Tests asserting branch-specific shape live in keyword_query_test.go.
func fallbackTestAnalyzer() stubAnalyzer {
	return stubAnalyzer{err: errors.New("test: analyze unavailable")}
}

// extractDSL serialises a query for asserting structural shape in tests.
func extractDSL(t *testing.T, q interface {
	Source() (any, error)
}) map[string]any {
	t.Helper()
	src, err := q.Source()
	if err != nil {
		t.Fatalf("Source(): %v", err)
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestBuildSearchMessagesDSL_Shape(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypeGroup,
		ChannelID:   "groupNo",
		Keyword:     "hello",
		Filters: SearchFilters{
			SenderIDs: []string{"u1", "u2"},
		},
	}
	// Fallback analyzer keeps the original keyword in the emitted query, so
	// the historical substring assertions (the "hello" pin) still apply. The
	// MSM 75% + cross_fields shape introduced by the OR-trap fix is asserted
	// in keyword_query_test.go alongside the branch logic — duplicating it
	// here would just couple this test to the fallback path.
	q, _ := buildSearchMessagesDSL(context.Background(), fallbackTestAnalyzer(), true, req, "groupNo", "")
	dsl := extractDSL(t, q.(interface {
		Source() (any, error)
	}))
	js, _ := json.Marshal(dsl)
	body := string(js)
	for _, want := range []string{
		`"multi_match"`,
		`"hello"`,
		`"payload.text.content^3"`,
		`"payload.mergeForward.msgs.searchText"`,
		`"channelId":"groupNo"`,
		`"revoked":true`,
		`"payload.type":99`,
		`"from":1000`,
		`"to":2000`,
		`"include_lower":true`,
		`"include_upper":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("DSL missing %q in:\n%s", want, body)
		}
	}
}

func TestBuildSearchMessagesDSL_NoKeywordSkipsMultiMatch(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypeGroup,
		ChannelID:   "groupNo",
	}
	q, _ := buildSearchMessagesDSL(context.Background(), fallbackTestAnalyzer(), true, req, "groupNo", "")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if strings.Contains(body, "multi_match") {
		t.Errorf("search_messages DSL with empty keyword must not include multi_match:\n%s", body)
	}
	for _, want := range []string{
		`"channelId":"groupNo"`,
		`"revoked":true`,
		`"payload.type":99`,
		`"from":1000`,
		`"to":2000`,
		`"include_lower":true`,
		`"include_upper":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty-keyword DSL missing %q in:\n%s", want, body)
		}
	}
}

// TestBuildSearchMessagesDSL_FiltersSystemMessages pins the indexer §2.2
// "搜索硬过滤" contract: payload.type 1000-2000 (FriendApply / Group* / Hotline*
// / Tip) MUST be excluded from /_search_messages, alongside the existing
// type==99 (Cmd) exclusion. Regression test for the empty-keyword browse path
// leaking "GroupCreate" / "GroupMemberAdd" system events to the client.
func TestBuildSearchMessagesDSL_FiltersSystemMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keyword string
	}{
		{"keyword", "hello"},
		{"browse", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := SearchMessagesReq{
				ChannelType: channelTypeGroup,
				ChannelID:   "groupNo",
				Keyword:     tc.keyword,
			}
			q, _ := buildSearchMessagesDSL(context.Background(), fallbackTestAnalyzer(), true, req, "groupNo", "")
			src, err := q.(interface{ Source() (any, error) }).Source()
			if err != nil {
				t.Fatalf("Source(): %v", err)
			}
			b, _ := json.Marshal(src)
			body := string(b)

			// JSON-roundtrip so all numeric values normalize to float64 and
			// nested objects to map[string]any, regardless of what concrete
			// Go types elastic.BoolQuery.Source() chose to emit.
			var normalized map[string]any
			if err := json.Unmarshal(b, &normalized); err != nil {
				t.Fatalf("normalize: %v", err)
			}
			boolNode, ok := normalized["bool"].(map[string]any)
			if !ok {
				t.Fatalf("query has no bool node: %s", body)
			}
			rawMN, ok := boolNode["must_not"]
			if !ok {
				t.Fatalf("bool has no must_not: %s", body)
			}
			var mustNot []any
			switch v := rawMN.(type) {
			case []any:
				mustNot = v
			case map[string]any:
				mustNot = []any{v}
			default:
				t.Fatalf("must_not has unexpected shape %T: %s", rawMN, body)
			}

			var seenCmd, seenRange bool
			for _, clause := range mustNot {
				m, ok := clause.(map[string]any)
				if !ok {
					continue
				}
				if term, ok := m["term"].(map[string]any); ok {
					if pt, ok := term["payload.type"].(float64); ok && int(pt) == payloadTypeCmd {
						seenCmd = true
					}
				}
				if rng, ok := m["range"].(map[string]any); ok {
					if pt, ok := rng["payload.type"].(map[string]any); ok {
						// elastic encodes Gte/Lte as from/to + include_lower/include_upper.
						lo, loOK := pt["from"].(float64)
						hi, hiOK := pt["to"].(float64)
						incLo, _ := pt["include_lower"].(bool)
						incHi, _ := pt["include_upper"].(bool)
						if loOK && hiOK && int(lo) == payloadTypeSystemMin && int(hi) == payloadTypeSystemMax && incLo && incHi {
							seenRange = true
						}
					}
				}
			}
			if !seenCmd {
				t.Errorf("must_not missing term payload.type=%d in:\n%s", payloadTypeCmd, body)
			}
			if !seenRange {
				t.Errorf("must_not missing range payload.type [%d,%d] in:\n%s", payloadTypeSystemMin, payloadTypeSystemMax, body)
			}
		})
	}
}

// TestApplySystemMessageHardFilter pins the shared helper that both
// /_search_messages and /_search_around use to satisfy the indexer §2.4
// "搜索硬过滤" contract. Empty bool query in, two must_not clauses out:
// term(payload.type=99) and range(payload.type ∈ [1000, 2000]).
func TestApplySystemMessageHardFilter(t *testing.T) {
	b := elastic.NewBoolQuery()
	applySystemMessageHardFilter(b)

	src, err := b.Source()
	if err != nil {
		t.Fatalf("Source(): %v", err)
	}
	raw, _ := json.Marshal(src)
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	boolNode, ok := normalized["bool"].(map[string]any)
	if !ok {
		t.Fatalf("query has no bool node: %s", raw)
	}
	rawMN, ok := boolNode["must_not"]
	if !ok {
		t.Fatalf("bool has no must_not: %s", raw)
	}
	var mustNot []any
	switch v := rawMN.(type) {
	case []any:
		mustNot = v
	case map[string]any:
		mustNot = []any{v}
	default:
		t.Fatalf("must_not has unexpected shape %T: %s", rawMN, raw)
	}

	var seenCmd, seenRange bool
	for _, clause := range mustNot {
		m, ok := clause.(map[string]any)
		if !ok {
			continue
		}
		if term, ok := m["term"].(map[string]any); ok {
			if pt, ok := term["payload.type"].(float64); ok && int(pt) == payloadTypeCmd {
				seenCmd = true
			}
		}
		if rng, ok := m["range"].(map[string]any); ok {
			if pt, ok := rng["payload.type"].(map[string]any); ok {
				lo, loOK := pt["from"].(float64)
				hi, hiOK := pt["to"].(float64)
				incLo, _ := pt["include_lower"].(bool)
				incHi, _ := pt["include_upper"].(bool)
				if loOK && hiOK && int(lo) == payloadTypeSystemMin && int(hi) == payloadTypeSystemMax && incLo && incHi {
					seenRange = true
				}
			}
		}
	}
	if !seenCmd {
		t.Errorf("must_not missing term payload.type=%d in:\n%s", payloadTypeCmd, raw)
	}
	if !seenRange {
		t.Errorf("must_not missing range payload.type [%d,%d] in:\n%s", payloadTypeSystemMin, payloadTypeSystemMax, raw)
	}
}

func TestBuildSearchMediaDSL_FiltersTypes(t *testing.T) {
	req := SearchMediaReq{ChannelType: channelTypeGroup, ChannelID: "g"}
	q := buildSearchMediaDSL(req, "g", "")
	dsl := extractDSL(t, q.(interface {
		Source() (any, error)
	}))
	js, _ := json.Marshal(dsl)
	body := string(js)
	if !strings.Contains(body, `"payload.type":[2,5]`) && !strings.Contains(body, `"payload.type":[2, 5]`) {
		t.Errorf("media DSL should filter on payload.type [2,5]:\n%s", body)
	}
	if strings.Contains(body, "multi_match") {
		t.Errorf("media DSL must not include multi_match")
	}
}

func TestBuildSearchFilesDSL_NoKeywordSkipsMultiMatch(t *testing.T) {
	req := SearchFilesReq{ChannelType: channelTypeGroup, ChannelID: "g"}
	q, _ := buildSearchFilesDSL(context.Background(), fallbackTestAnalyzer(), true, req, "g", "")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if strings.Contains(body, "multi_match") {
		t.Errorf("file DSL with empty keyword must not include multi_match:\n%s", body)
	}
	if !strings.Contains(body, `"payload.type":8`) {
		t.Errorf("file DSL must filter type=8:\n%s", body)
	}
}

func TestBuildSearchFilesDSL_KeywordIncludesMultiMatch(t *testing.T) {
	req := SearchFilesReq{ChannelType: channelTypeGroup, ChannelID: "g", Keyword: "report"}
	q, _ := buildSearchFilesDSL(context.Background(), fallbackTestAnalyzer(), true, req, "g", "")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if !strings.Contains(body, `"multi_match"`) {
		t.Errorf("file DSL with keyword should include multi_match:\n%s", body)
	}
	if !strings.Contains(body, "payload.file.name^2") {
		t.Errorf("file DSL with keyword should target payload.file.name^2:\n%s", body)
	}
}

func TestBuildSearchAllDSL_TypeFilter(t *testing.T) {
	req := SearchMessagesReq{ChannelType: channelTypeGroup, ChannelID: "g", Keyword: "k"}
	q, _ := buildSearchAllDSL(context.Background(), fallbackTestAnalyzer(), true, req, "g", "")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	for _, want := range []string{
		`"payload.type":[1,8,11]`,
		`"minimum_should_match":"1"`,
		`"payload.text.content^3"`,
		`"payload.file.name^2"`,
	} {
		if !strings.Contains(body, want) && !strings.Contains(body, strings.ReplaceAll(want, ",", ", ")) {
			t.Errorf("search_all DSL missing %q in:\n%s", want, body)
		}
	}
}

func TestBuildSearchAllDSL_NoKeywordKeepsTypeFilter(t *testing.T) {
	req := SearchMessagesReq{ChannelType: channelTypeGroup, ChannelID: "g"}
	q, _ := buildSearchAllDSL(context.Background(), fallbackTestAnalyzer(), true, req, "g", "")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if strings.Contains(body, "multi_match") {
		t.Errorf("search_all DSL with empty keyword must not include multi_match:\n%s", body)
	}
	if strings.Contains(body, "minimum_should_match") {
		t.Errorf("search_all DSL with empty keyword must not include minimum_should_match:\n%s", body)
	}
	if strings.Contains(body, `"should"`) {
		t.Errorf("search_all DSL with empty keyword must not include a should clause:\n%s", body)
	}
	for _, want := range []string{
		`"channelId":"g"`,
		`"revoked":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty-keyword search_all DSL missing %q in:\n%s", want, body)
		}
	}
	// type filter must still segment message vs file
	if !strings.Contains(body, `"payload.type":[1,8,11]`) && !strings.Contains(body, `"payload.type":[1, 8, 11]`) {
		t.Errorf("empty-keyword search_all DSL must still filter payload.type [1,8,11]:\n%s", body)
	}
}

func TestExtractSortValues(t *testing.T) {
	ts, msg, score := extractSortValues([]any{float64(1717000000), float64(9876543210)}, false)
	if ts != 1717000000 || msg != 9876543210 {
		t.Fatalf("got ts=%d msgID=%d", ts, msg)
	}
	if score != nil {
		t.Fatalf("time_* sort should yield score=nil, got %v", *score)
	}
	if ts, msg, score := extractSortValues(nil, false); ts != 0 || msg != 0 || score != nil {
		t.Fatalf("nil sort should give zeros, got %d %d %v", ts, msg, score)
	}
}

func TestExtractSortValues_Relevance(t *testing.T) {
	// relevance sort emits [timestamp, _score, messageId]
	ts, msg, score := extractSortValues([]any{float64(1717000000), float64(12.5), float64(9876543210)}, true)
	if ts != 1717000000 || msg != 9876543210 {
		t.Fatalf("got ts=%d msgID=%d", ts, msg)
	}
	if score == nil || *score != 12.5 {
		t.Fatalf("expected score=12.5, got %v", score)
	}
	// short sort under relevance returns zeros + nil
	if ts, msg, score := extractSortValues([]any{float64(1), float64(2)}, true); ts != 0 || msg != 0 || score != nil {
		t.Fatalf("short relevance sort should give zeros, got %d %d %v", ts, msg, score)
	}
}

func TestNumericTo64_JSONNumber(t *testing.T) {
	// json.Number must keep full int64 precision — this is the path a
	// NumberDecoder-configured client would produce for snowflake IDs.
	const big = int64(1817958721236045824)
	if got := numericTo64(json.Number("1817958721236045824")); got != big {
		t.Fatalf("json.Number precision lost: got %d want %d", got, big)
	}
	if got := numericToFloat(json.Number("12.5")); got != 12.5 {
		t.Fatalf("json.Number float: got %v want 12.5", got)
	}
}

// searchResultWithLastHit builds a minimal one-hit SearchResult whose Sort
// array carries float64 values (the default-decoder shape) and whose _source
// carries the full-precision messageId.
func searchResultWithLastHit(t *testing.T, msgID int64, ts int64) *elastic.SearchResult {
	t.Helper()
	src := json.RawMessage([]byte(`{"messageId":` + strconv.FormatInt(msgID, 10) + `,"timestamp":` + strconv.FormatInt(ts, 10) + `}`))
	return &elastic.SearchResult{
		Hits: &elastic.SearchHits{
			Hits: []*elastic.SearchHit{
				{
					// Default json.Unmarshal decodes sort numbers as float64,
					// which rounds above 2^53 — exactly the corruption the
					// cursor must not inherit.
					Sort:   []any{float64(ts), float64(msgID)},
					Source: &src,
				},
			},
		},
	}
}

// TestComputeCursorPagination_SnowflakeMessageIDPrecision is the regression
// test for the P1 review finding: messageId is a snowflake (> 2^53), the Sort
// array arrives as float64 and rounds it, and the encoded cursor must still
// carry the exact id (taken from the typed _source) or pagination skips /
// duplicates messages at timestamp-tied boundaries.
func TestComputeCursorPagination_SnowflakeMessageIDPrecision(t *testing.T) {
	const snowflake = int64(1817958721236045827) // > 2^53; int64(float64(x)) != x
	if int64(float64(snowflake)) == snowflake {
		t.Fatalf("test value must lose precision through float64 to be meaningful")
	}
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	h := &Handler{cfg: cfg}

	result := searchResultWithLastHit(t, snowflake, 1717000000)
	hasMore, cursor := h.computeCursorPagination(result, 1, "time_desc")
	if !hasMore || cursor == "" {
		t.Fatalf("expected has_more with cursor, got %v %q", hasMore, cursor)
	}
	ts, msgID, score, err := decodeCursor(cfg, cursor)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if msgID != snowflake {
		t.Fatalf("cursor messageId lost precision: got %d want %d", msgID, snowflake)
	}
	if ts != 1717000000 {
		t.Fatalf("cursor ts: got %d", ts)
	}
	if score != nil {
		t.Fatalf("time_desc cursor should carry no score")
	}
}

// TestComputeCursorPagination_BadSourceNoCursor pins the fail-safe: when the
// last hit's _source cannot provide a messageId we suppress the cursor
// entirely instead of emitting a corrupt one.
func TestComputeCursorPagination_BadSourceNoCursor(t *testing.T) {
	h := &Handler{cfg: SearchConfig{CursorHMAC: "k"}}
	bad := json.RawMessage([]byte(`not-json`))
	result := &elastic.SearchResult{
		Hits: &elastic.SearchHits{
			Hits: []*elastic.SearchHit{
				{Sort: []any{float64(1717000000), float64(123)}, Source: &bad},
			},
		},
	}
	hasMore, cursor := h.computeCursorPagination(result, 1, "time_desc")
	if hasMore || cursor != "" {
		t.Fatalf("bad _source must suppress cursor, got %v %q", hasMore, cursor)
	}
}
