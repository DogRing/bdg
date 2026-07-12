package api

import (
	"log"
	"net/http"
	"time"
)

const (
	xreadBlock = 3 * time.Second // XRead BLOCK duration; bounds SSE drain on graceful shutdown.
	sseStartID = "$"             // No client cursor ⇒ tail new events only (no backlog replay).

	// streamGapFrame is the one control frame sent (WITHOUT an id: line, so the
	// client's Last-Event-ID is not disturbed) when the requested replay cursor
	// is older than the stream's retained backlog: entries after the cursor
	// were trimmed away, so a replay would be a silently-partial sparse
	// history. The client must reacquire a fresh snapshot/cursor pair
	// (frontend/SPEC.md §Bootstrap; data-contracts §4). The handler closes the
	// stream after sending it.
	streamGapFrame = `data: {"schema_version":1,"tick":0,"seq":0,"agent_id":null,"type":"StreamGap","payload":{"reason":"cursor_trimmed"}}` + "\n\n"
)

// clientCursor extracts the replay cursor from the request: the standard
// Last-Event-ID header (browser reconnect) wins over the explicit ?cursor=
// query (bootstrap). Malformed values are ignored (⇒ live tail) — a cursor is
// an optimization contract, never an error surface.
func clientCursor(r *http.Request) string {
	if c := r.Header.Get("Last-Event-ID"); validStreamID(c) {
		return c
	}
	if c := r.URL.Query().Get("cursor"); validStreamID(c) {
		return c
	}
	return ""
}

// handleSSE tails the Redis events STREAM and forwards each entry as an SSE frame.
// Wire detail (SPEC §Routes GET /sse):
//   - Sets Content-Type / Cache-Control / X-Accel-Buffering / Connection before the first write.
//   - Every frame carries the Redis entry ID as its standard `id:` line — the transport cursor
//     (data-contracts §4). Browsers resend it as Last-Event-ID on auto-reconnect; the frontend
//     passes it explicitly as ?cursor= on manual (re)connects.
//   - REPLAY: with a valid client cursor the loop starts XREADing strictly AFTER it, so retained
//     entries between a snapshot's stream_cursor and the connection are replayed and flow
//     gap-free into the live tail (one loop ⇒ no duplicates). Without a cursor it tails from "$"
//     (live only; a fresh connect never replays the whole backlog).
//   - TRIM/GAP: before replaying, the cursor is checked against the stream's
//     max-deleted-entry-id — if entries after the cursor were trimmed, ONE StreamGap control
//     frame is sent and the stream is CLOSED (client must resnapshot). A regen-recreated stream
//     resets that metadata, so a post-regen cursor referencing deleted old-world entries is NOT
//     a gap (nothing after it was lost) and old-world entries can never be replayed.
//   - Blocks on XRead with a short timeout so ctx cancellation (client disconnect or shutdown)
//     is observed promptly — no goroutine outlives the request.
//   - Flushes after every event (one Flush per entry, SPEC invariant).
//   - Forwards fields["payload"] verbatim; does NOT re-marshal (the bytes are already the
//     JSON-serialised core.Event written by platform/events, data-contracts §4).
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	evKey := s.keyer.Events()
	lastID := sseStartID

	if cursor := clientCursor(r); cursor != "" {
		maxDeleted, err := s.rds.StreamMaxDeletedID(ctx, evKey)
		if err != nil {
			// Trim detection is best-effort: without XINFO we cannot verify the
			// backlog, so fall back to the live tail rather than risk serving a
			// silently-partial replay OR failing the connection.
			log.Printf("api/sse: StreamMaxDeletedID: %v (cursor %s ignored, live tail)", err, cursor)
		} else if validStreamID(maxDeleted) && streamIDLess(cursor, maxDeleted) {
			// Entries AFTER the cursor were deleted/trimmed: the retained
			// backlog has a hole. Signal and close (SPEC §Routes GET /sse).
			if _, werr := w.Write([]byte(streamGapFrame)); werr == nil {
				flusher.Flush()
			}
			return
		} else {
			lastID = cursor
		}
	}

	for {
		entries, newLastID, err := s.rds.XRead(ctx, evKey, lastID, xreadBlock)
		if err != nil {
			if ctx.Err() != nil {
				return // clean client disconnect or shutdown
			}
			log.Printf("api/sse: XRead error (lastID=%s): %v", lastID, err)
			continue
		}
		lastID = newLastID
		for _, entry := range entries {
			payload := entry.Fields["payload"]
			if payload == "" {
				continue
			}
			if _, err := w.Write([]byte("id: " + entry.ID + "\ndata: " + payload + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
			lastID = entry.ID
		}
	}
}
