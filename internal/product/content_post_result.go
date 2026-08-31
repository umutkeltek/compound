package product

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// content_post_result.go closes the loop between a governed outbound post and the
// draft it came from.
//
// Before this, `act post --draft` executed the transport, printed a receipt, and
// left the draft sitting at "operationalized" forever. Nothing in the system could
// answer "did it actually go out?", so the weekly distribution driver measured the
// only thing it could see — whether a draft had been WRITTEN — and reported
// `weekly_floor_met` every day while zero posts had ever been published.
//
// The transport's own stdout is the post-image: the remote platform's answer. That
// answer is where a real post id lives, and a post id is the only evidence that
// separates "we sent it" from "we think we sent it".

// contentPostRemoteIDKeys are the field names a post transport may use for the
// remote object id, in priority order. The set is small and explicit rather than
// a loose search: a transport that returns none of these has not proven a post.
var contentPostRemoteIDKeys = []string{"remote_id", "post_id", "postId", "id"}

// contentPostURLKeys are the field names a post transport may use for the public
// permalink. A URL is useful for a human but is NOT accepted as proof of posting —
// only a remote id is.
var contentPostURLKeys = []string{"url", "permalink", "releaseURL", "postUrl"}

// RecordContentPostResult reads a transport's post-image and moves the draft to the
// terminal "posted" state with the platform's own id attached.
//
// It deliberately fails LOUDLY when the post-image carries no remote id. The post
// may well have gone out, and the caller must surface that rather than swallow it:
// a silent failure here would leave the draft looking unpublished forever, which is
// the same measurement lie this state was added to end.
func RecordContentPostResult(homePath, draftID string, postImage []byte, receiptRef string, now time.Time) (ContentDraft, error) {
	remoteID, url, err := parseContentPostImage(postImage)
	if err != nil {
		return ContentDraft{}, err
	}
	return MarkContentDraftPosted(homePath, draftID, ContentDraftPostInput{
		RemoteID:   remoteID,
		URL:        url,
		ReceiptRef: receiptRef,
	}, now)
}

// parseContentPostImage extracts the remote id and permalink from a transport's
// stdout. The transport contract is a single JSON object; anything else cannot
// prove a post happened.
func parseContentPostImage(postImage []byte) (remoteID, url string, err error) {
	trimmed := strings.TrimSpace(string(postImage))
	if trimmed == "" {
		return "", "", fmt.Errorf("transport returned no post-image: a post cannot be recorded without the platform's answer")
	}
	// UseNumber, not the default float64: a snowflake-style id like
	// 1730000000000000123 does not survive a float64 round trip, and an id that
	// lost its last digits is worse than no id — it looks provable and is not.
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return "", "", fmt.Errorf("transport post-image is not a JSON object, so it carries no provable remote id: %w", err)
	}
	remoteID = firstStringField(payload, contentPostRemoteIDKeys)
	if remoteID == "" {
		return "", "", fmt.Errorf("transport post-image carries no remote id (looked for %s): the post may have gone out, but nothing proves it", strings.Join(contentPostRemoteIDKeys, ", "))
	}
	return remoteID, firstStringField(payload, contentPostURLKeys), nil
}

// firstStringField returns the first non-empty string value among keys. Numeric ids
// are accepted too — platforms return them both ways — but only via a lossless
// integer rendering, never a float that could mangle a large id.
func firstStringField(payload map[string]any, keys []string) string {
	for _, key := range keys {
		switch v := payload[key].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case json.Number:
			// A fractional "id" is not an id. json.Number keeps every digit of a
			// large integer id verbatim, which float64 would silently round off.
			if s := strings.TrimSpace(v.String()); s != "" && !strings.Contains(s, ".") {
				return s
			}
		}
	}
	return ""
}
