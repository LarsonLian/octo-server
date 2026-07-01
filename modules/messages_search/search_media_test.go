package messages_search

import (
	"strconv"
	"testing"
)

func TestBuildMediaHits_ImageThumbURLFromOriginalURL(t *testing.T) {
	tp := payloadTypeImage
	doc := Doc{
		MessageID:  100,
		MessageSeq: 7,
		From:       "u1",
		Timestamp:  1717000000,
		Payload: &Payload{
			Type:  &tp,
			Image: &ImagePayload{URL: "http://example.com/a.png", Width: 200, Height: 100},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleMediaHit(doc, SearchMediaReq{ChannelType: channelTypeGroup, ChannelID: "g"})
	if got.MediaKind != "image" {
		t.Errorf("media_kind: got %q", got.MediaKind)
	}
	// v1.8 mapping has no thumb_url field; BFF surfaces image URL so
	// callers always have a renderable URL (frontend may add CDN sizing).
	if got.ThumbURL != "http://example.com/a.png" {
		t.Errorf("thumb_url should be image URL, got %q", got.ThumbURL)
	}
	if got.Width != 200 || got.Height != 100 {
		t.Errorf("dims: %+v", got)
	}
	if got.DurationMs != 0 {
		t.Errorf("duration_ms must be 0 for images, got %d", got.DurationMs)
	}
	if got.MessageID != strconv.FormatInt(100, 10) {
		t.Errorf("message_id: got %q", got.MessageID)
	}
}

func TestBuildMediaHits_VideoCoverIsThumbAndSecondToMs(t *testing.T) {
	tp := payloadTypeVideo
	doc := Doc{
		Payload: &Payload{
			Type:  &tp,
			Video: &VideoPayload{URL: "https://cdn/v.mp4", Cover: "cover.jpg", Second: 5, Width: 1920, Height: 1080},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleMediaHit(doc, SearchMediaReq{ChannelType: channelTypeGroup, ChannelID: "g"})
	if got.ThumbURL != "cover.jpg" {
		t.Errorf("expected cover as thumb_url, got %q", got.ThumbURL)
	}
	if got.VideoURL != "https://cdn/v.mp4" {
		t.Errorf("expected payload.video.url as video_url, got %q", got.VideoURL)
	}
	// v1.8: indexer stores second (s); BFF surfaces duration_ms (ms).
	if got.DurationMs != 5000 {
		t.Errorf("duration_ms: got %d want 5000", got.DurationMs)
	}
	if got.MediaKind != "video" {
		t.Errorf("media_kind: got %q", got.MediaKind)
	}
}

func TestBuildMediaHits_VideoZeroSecondOmitsDuration(t *testing.T) {
	tp := payloadTypeVideo
	doc := Doc{
		Payload: &Payload{
			Type:  &tp,
			Video: &VideoPayload{Cover: "cover.jpg"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleMediaHit(doc, SearchMediaReq{ChannelType: channelTypeGroup, ChannelID: "g"})
	if got.DurationMs != 0 {
		t.Errorf("missing second should yield duration_ms=0, got %d", got.DurationMs)
	}
}

// Video without a cover still surfaces video_url — the play URL is the
// primary renderable, cover is only a poster.
func TestBuildMediaHits_VideoNoCoverStillHasVideoURL(t *testing.T) {
	tp := payloadTypeVideo
	doc := Doc{
		Payload: &Payload{
			Type:  &tp,
			Video: &VideoPayload{URL: "https://cdn/v.mp4"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleMediaHit(doc, SearchMediaReq{ChannelType: channelTypeGroup, ChannelID: "g"})
	if got.ThumbURL != "" {
		t.Errorf("thumb_url should be empty when cover missing, got %q", got.ThumbURL)
	}
	if got.VideoURL != "https://cdn/v.mp4" {
		t.Errorf("video_url must be populated even without cover, got %q", got.VideoURL)
	}
}
