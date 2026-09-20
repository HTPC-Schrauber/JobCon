package api

import (
	"fmt"
	"jobcon/internal/runner"
	"net/http"
)

// streamLogsSSE streams log lines via Server-Sent Events to the client
func streamLogsSSE(w http.ResponseWriter, r *http.Request, broadcaster *runner.LogBroadcaster) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx proxy buffering

	ch, buffer, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	// Replay past lines
	for _, line := range buffer {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case line, ok := <-ch:
			if !ok {
				// Execution ended and broadcaster closed
				fmt.Fprintf(w, "event: end\ndata: [JobCon] Execution stream ended\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}
