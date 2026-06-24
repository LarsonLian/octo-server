# messages_search: hard-filter system messages from `_search_messages` and `_search_around`

Date: 2026-06-23
Surface: `POST /v1/messages/_search` (`_search_messages` reader path),
`POST /v1/messages/_search_around` (anchor + window)

## 1. Problem

Empty-keyword "browse" mode on `/v1/messages/_search` (legacy `_search` surface)
recalled control-plane events — "GroupCreate", "GroupMemberAdd", "Tip",
"FriendApply", etc. — instead of being limited to user-authored content. This
violates the indexer's "搜索硬过滤" contract for the 1000-2000 system-message
range.

Follow-up review found the same gap in `/_search_around`: both the anchor
lookup and the window query only excluded `payload.type == 99`, so an anchor on
a system event could be located and the surrounding window would surface
control-plane noise. The around path was not believed to be wired up from the
current frontend, but the endpoint is mounted and the fix is cheap, so it is
folded into this remediation.

## 2. Root cause

`buildSearchMessagesDSL` (and, as it turned out on review, `buildAnchorDSL` and
`buildAroundDSL`) only excluded `payload.type == 99` (Cmd):

```go
b.MustNot(elastic.NewTermQuery("payload.type", payloadTypeCmd))
```

None of them excluded the **1000-2000 system range** defined by the indexer
(`FriendApply=1000` … `Tip=2000`). When the keyword is empty, the only
discriminator left is the channel filter + `revoked=false`, so every system
event indexed in the channel surfaced as a "hit". For around, the same hole
exists structurally: window queries have no keyword by design, so the system
range bleed-through is unconditional.

## 3. Fix

Add a single `RangeQuery` to `must_not` alongside the existing cmd term, on
both surfaces, via a shared helper (see §7):

```go
applySystemMessageHardFilter(b)
// ↓ expands to
// b.MustNot(elastic.NewTermQuery("payload.type", payloadTypeCmd))
// b.MustNot(elastic.NewRangeQuery("payload.type").Gte(payloadTypeSystemMin).Lte(payloadTypeSystemMax))
```

Constants `payloadTypeSystemMin = 1000` / `payloadTypeSystemMax = 2000` are
added to `modules/messages_search/source.go` alongside the existing
`payloadType*` family, with a comment that points back to the indexer spec
(§2.4) as the source of truth.

## 4. Affected endpoints

| Endpoint | Status | Why |
|---|---|---|
| `POST /v1/messages/_search` (`_search_messages`) | **Fixed** — adds 1000-2000 must_not range | Keyword + browse surface; only one that previously matched system events from the user-facing path |
| `POST /v1/messages/_search_around` | **Fixed in this PR (continuation)** — `buildAnchorDSL` + `buildAroundDSL` both pick up the range via shared helper | Window query carries no keyword by design, so the system-range bleed-through is structural; anchor lookup must hold to the same contract so a system event is never a valid anchor |
| `POST /v1/messages/_search_all` | Unchanged — already safe | Whitelist filter `terms payload.type in [1, 8, 11]` excludes anything outside text/file/mergeForward |
| `POST /v1/messages/_search_files` | N/A | Hard-filters `payload.type == 8` |
| `POST /v1/messages/_search_media` | N/A | Hard-filters `payload.type in [2, 5]` |

## 5. Tests

- `TestApplySystemMessageHardFilter` (new, `dsl_test.go`): unit test for the
  shared helper — walks the parsed bool query and asserts both must_not
  clauses are emitted given an empty `BoolQuery` input.
- `TestBuildSearchMessagesDSL_FiltersSystemMessages` (existing, `dsl_test.go`):
  asserts both the term (`payload.type == 99`) and range (`payload.type ∈
  [1000, 2000]`) clauses are emitted in `must_not`, in both keyword and
  empty-keyword (browse) branches. Walks the parsed query tree rather than
  relying on substring pins so the assertion survives unrelated DSL
  formatting changes.
- `TestBuildAroundDSL_FiltersSystemMessages` (new, `search_around_test.go`):
  same assertion against `buildAroundDSL` — the around window.
- `TestBuildAnchorDSL_FiltersSystemMessages` (new, `search_around_test.go`):
  same assertion against `buildAnchorDSL` — the around anchor lookup.
- `TestBuildSearchMessagesDSL_Shape` /
  `TestBuildSearchMessagesDSL_NoKeywordSkipsMultiMatch` continue to require
  `"gte":1000` / `"lte":2000` in the emitted DSL — keeps the literal-JSON
  pins in sync without dropping the existing `payload.type=99` pin.
- Staging verification (TODO): seed a channel with a `GroupMemberAdd`
  (`payload.type=1002`) event and a regular text message, hit
  `/_search_messages` with empty keyword AND `/_search_around` anchored
  before/after the event, confirm only the text message is returned in
  either surface.

## 6. References

- Indexer spec §2.4 (search hard-filter contract):
  `~/Projects/_refs/wukongim-message-indexer/docs/specs/2026-06-04-v1.6-decisions.md`
- Code: `modules/messages_search/dsl.go::applySystemMessageHardFilter`,
  `modules/messages_search/search_messages.go::buildSearchMessagesDSL`,
  `modules/messages_search/search_around.go::buildAnchorDSL` /
  `buildAroundDSL`, `modules/messages_search/source.go`
  (`payloadTypeSystemMin`/`Max`).

## 7. Shared helper

The fix factors the two `must_not` clauses into
`applySystemMessageHardFilter(*elastic.BoolQuery)` in `dsl.go`. Rationale:

- The contract is defined once by the indexer spec (§2.4) and must hold on
  both `_search_messages` and `_search_around`. Duplicating the literal
  `MustNot(...)` calls in three call sites (search_messages window,
  search_around window, search_around anchor) created the original drift —
  the around builders were written with only the cmd term while
  `_search_messages` got both clauses. The helper removes that drift surface.
- Scope is deliberately narrow: the helper covers ONLY the
  `payload.type`-based negations. `revoked` is a message-level state — a
  different semantic layer — and stays in `applyChannelAndRevoked`. Callers
  on a search/browse path are expected to call both.
- When the indexer pins down the RTC range (§8 below) the helper picks up
  the extra clause and all three call sites benefit at once.

## 8. RTC 9989-9999 follow-up (TODO)

The indexer spec §2.3 reserves a Webhook / RTC range (provisionally
`9989-9999`) that is not yet finalised by the indexer owners. Once §2.3 is
pinned, `applySystemMessageHardFilter` should add the corresponding
`must_not` clause (likely another `RangeQuery`) so both surfaces pick it up
at the same time. This PR intentionally does NOT add the clause — adding a
speculative range filter risks dropping legitimate hits if the boundary
moves.

