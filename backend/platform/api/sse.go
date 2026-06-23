package api

import (
	"log"
	"net/http"
	"time"
)

const (
	xreadBlock = 3 * time.Second // XRead BLOCK duration; bounds SSE drain on graceful shutdown.
	sseStartID = "$"             // Start tail from new events only; no replay on fresh connect.
)

// handleSSE tails the Redis events STREAM and forwards each entry as an SSE frame.
// Wire detail (SPEC §SSE wire detail):
//   - Sets Content-Type / Cache-Control / X-Accel-Buffering / Connection before the first write.
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
			if _, err := w.Write([]byte("data: " + payload + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
			lastID = entry.ID
		}
	}
}
