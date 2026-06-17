package messages_search

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
)

// newValidateCtx builds a minimal wkhttp.Context whose writes land in a
// recorder so validateBase's error envelope can be inspected.
func newValidateCtx(t *testing.T) (*wkhttp.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(rec)
	gc.Request = httptest.NewRequest("POST", "/v1/messages/_search", nil)
	return &wkhttp.Context{Context: gc}, rec
}

// V7 (page-size half) — page_size above the 100 ceiling must be rejected with
// VALIDATION_ERROR rather than silently clamped (a silent clamp would let a
// caller think they paged 500 and miss results). Mirrors the plan's
// "page-size 上限 100" gate.
func TestValidateBase_PageSizeOverMaxRejected(t *testing.T) {
	c, rec := newValidateCtx(t)
	_, ok := validateBase(c, SearchConfig{}, channelTypeGroup, "G1", "", "",
		SearchFilters{}, maxPageSize+1, true)
	if ok {
		t.Fatalf("page_size %d (> %d) must be rejected", maxPageSize+1, maxPageSize)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("rejected page_size must render a VALIDATION_ERROR envelope")
	}
}

func TestValidateBase_PageSizeZeroDefaults(t *testing.T) {
	c, _ := newValidateCtx(t)
	page, ok := validateBase(c, SearchConfig{}, channelTypeGroup, "G1", "", "",
		SearchFilters{}, 0, true)
	if !ok {
		t.Fatalf("page_size 0 must default, not reject")
	}
	if page != defaultPage {
		t.Fatalf("page_size 0 must default to %d, got %d", defaultPage, page)
	}
}

func TestValidateBase_PageSizeAtMaxAccepted(t *testing.T) {
	c, _ := newValidateCtx(t)
	page, ok := validateBase(c, SearchConfig{}, channelTypeGroup, "G1", "", "",
		SearchFilters{}, maxPageSize, true)
	if !ok {
		t.Fatalf("page_size at the %d ceiling must be accepted", maxPageSize)
	}
	if page != maxPageSize {
		t.Fatalf("page_size %d must pass through unchanged, got %d", maxPageSize, page)
	}
}

func TestValidateBase_PageSizeNegativeRejected(t *testing.T) {
	c, rec := newValidateCtx(t)
	_, ok := validateBase(c, SearchConfig{}, channelTypeGroup, "G1", "", "",
		SearchFilters{}, -1, true)
	if ok {
		t.Fatalf("negative page_size must be rejected")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("rejected page_size must render an error envelope")
	}
}

// V2-adjacent — channel_type outside the {1,2,5} whitelist is rejected before
// any access check runs (None/CS/Community/Info channels are not openable this
// phase; the plan says None must be explicitly refused).
func TestValidateBase_UnknownChannelTypeRejected(t *testing.T) {
	for _, ct := range []uint8{0, 3, 4, 99} {
		c, rec := newValidateCtx(t)
		_, ok := validateBase(c, SearchConfig{}, ct, "X", "", "",
			SearchFilters{}, 20, true)
		if ok {
			t.Fatalf("channel_type %d must be rejected", ct)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("channel_type %d rejection must render an error envelope", ct)
		}
	}
}

// A malformed cursor must surface VALIDATION_ERROR(field=cursor), never be
// silently treated as a first-page request (which would reset pagination and
// leak the first page on a tampered cursor).
func TestValidateBase_MalformedCursorRejected(t *testing.T) {
	c, rec := newValidateCtx(t)
	_, ok := validateBase(c, SearchConfig{}, channelTypeGroup, "G1", "", "not-a-valid-cursor!!",
		SearchFilters{}, 20, true)
	if ok {
		t.Fatalf("malformed cursor must be rejected")
	}
	if !strings.Contains(rec.Body.String(), "cursor") &&
		!strings.Contains(strings.ToLower(rec.Body.String()), "invalid") {
		t.Fatalf("cursor rejection must render a validation envelope, got %q", rec.Body.String())
	}
}

// sort=relevance with allowRelevance=false (the empty-keyword path) must be
// refused — relevance has no _score to sort on when the multi_match clause is
// dropped. The user-facing body is rendered through i18n templates so the
// raw reason string is not asserted here (other validate tests follow the
// same envelope-only pattern); the rejection itself is the contract.
func TestValidateBase_RelevanceRequiresKeyword(t *testing.T) {
	c, rec := newValidateCtx(t)
	_, ok := validateBase(c, SearchConfig{}, channelTypeGroup, "G1", "relevance", "",
		SearchFilters{}, 20, false)
	if ok {
		t.Fatalf("sort=relevance without keyword (allowRelevance=false) must be rejected")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("rejection must render a VALIDATION_ERROR envelope")
	}

	// And the symmetric positive: with allowRelevance=true (keyword present)
	// the same sort value passes — guards against regressing the gate.
	c2, _ := newValidateCtx(t)
	if _, ok := validateBase(c2, SearchConfig{}, channelTypeGroup, "G1", "relevance", "",
		SearchFilters{}, 20, true); !ok {
		t.Fatalf("sort=relevance with allowRelevance=true must be accepted")
	}
}

// Empty-search guard: empty keyword AND no effective filter must be rejected
// with a VALIDATION_ERROR — that combination degenerates into an unconditional
// full-channel scan that can pin OpenSearch.
func TestValidateSearchNotEmpty_EmptyKeywordEmptyFilter_Rejected(t *testing.T) {
	c, rec := newValidateCtx(t)
	if validateSearchNotEmpty(c, "", SearchFilters{}) {
		t.Fatalf("empty keyword + empty filter must be rejected")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("rejection must render a VALIDATION_ERROR envelope")
	}
}

// 🔴 Bypass point: a sender_ids list of only empty strings must NOT count as a
// filter. dsl.go::addCommonFilters trims empty ids and emits no terms clause,
// so `{"sender_ids":[""]}` with an empty keyword still degenerates into a full
// scan. The guard must reject it the same as a wholly empty filter.
func TestValidateSearchNotEmpty_EmptyKeywordBlankSenderIDs_Rejected(t *testing.T) {
	for _, senders := range [][]string{
		{""},
		{"", "  "},
		{"\t", " "},
	} {
		c, rec := newValidateCtx(t)
		if validateSearchNotEmpty(c, "", SearchFilters{SenderIDs: senders}) {
			t.Fatalf("empty keyword + blank sender_ids %v must be rejected (bypass point)", senders)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("rejection must render a VALIDATION_ERROR envelope for %v", senders)
		}
	}
}

// An empty keyword with at least one effective filter is allowed — this is the
// keyword-optional listing path the guard must NOT block.
func TestValidateSearchNotEmpty_EmptyKeywordEffectiveFilter_Accepted(t *testing.T) {
	cases := []SearchFilters{
		{SenderIDs: []string{"u1"}},
		{SenderIDs: []string{"", "u2"}},
		{SentAtFrom: "2026-01-01"},
		{SentAtTo: "2026-12-31"},
	}
	for _, f := range cases {
		c, _ := newValidateCtx(t)
		if !validateSearchNotEmpty(c, "", f) {
			t.Fatalf("empty keyword with effective filter %+v must be accepted", f)
		}
	}
}

// A non-empty keyword always passes the guard regardless of filters.
func TestValidateSearchNotEmpty_KeywordPresent_Accepted(t *testing.T) {
	c, _ := newValidateCtx(t)
	if !validateSearchNotEmpty(c, "hello", SearchFilters{}) {
		t.Fatalf("non-empty keyword must be accepted with no filters")
	}
}

// hasEffectiveFilters must agree with addCommonFilters on what "has a filter"
// means — blank-only sender_ids and untrimmed-empty time bounds are not
// effective.
func TestHasEffectiveFilters(t *testing.T) {
	tests := []struct {
		name    string
		filters SearchFilters
		want    bool
	}{
		{"empty", SearchFilters{}, false},
		{"blank sender", SearchFilters{SenderIDs: []string{""}}, false},
		{"whitespace sender", SearchFilters{SenderIDs: []string{"  ", "\t"}}, false},
		{"real sender", SearchFilters{SenderIDs: []string{"u1"}}, true},
		{"mixed sender", SearchFilters{SenderIDs: []string{"", "u1"}}, true},
		{"from set", SearchFilters{SentAtFrom: "2026-01-01"}, true},
		{"to set", SearchFilters{SentAtTo: "2026-01-01"}, true},
		{"blank from", SearchFilters{SentAtFrom: "   "}, false},
	}
	for _, tc := range tests {
		if got := hasEffectiveFilters(tc.filters); got != tc.want {
			t.Errorf("%s: hasEffectiveFilters=%v want %v", tc.name, got, tc.want)
		}
	}
}
