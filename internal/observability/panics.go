// Copyright 2026 Optiqor contributors
// SPDX-License-Identifier: Apache-2.0

// Package observability provides shared panic handling and crash-loop safety utilities.
package observability

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

const (
	panicLogDir       = "/var/log/kerno-panics"
	crashLoopWindow   = 10 * time.Minute
	crashLoopMaxCount = 5
)

// PanicHandler tracks panics and enforces crash-loop safety.
type PanicHandler struct {
	mu          sync.Mutex
	panicCounts map[string][]time.Time
}

// GlobalHandler is the shared instance of PanicHandler.
var GlobalHandler = &PanicHandler{
	panicCounts: make(map[string][]time.Time),
}

// HandlePanic processes a recovered panic. It writes the stack trace to a file,
// logs the error, and returns whether the collector should be permanently disabled
// due to crash-looping.
func (h *PanicHandler) HandlePanic(component string, r interface{}, logger *slog.Logger) bool {
	now := time.Now()

	// Create panic log directory if it doesn't exist
	if err := os.MkdirAll(panicLogDir, 0750); err != nil {
		logger.Error("failed to create panic log directory", "dir", panicLogDir, "error", err)
	}

	stack := debug.Stack()

	// Determine panic reason
	reason := "unknown"
	if err, ok := r.(error); ok {
		reason = err.Error()
	} else if s, ok := r.(string); ok {
		reason = s
	}

	// Write stack trace to a file
	filename := fmt.Sprintf("%s-%d.txt", component, now.Unix())
	path := filepath.Join(panicLogDir, filename)

	content := fmt.Sprintf("Time: %s\nComponent: %s\nPanic: %v\n\nStack:\n%s\n", now.Format(time.RFC3339), component, r, stack)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		logger.Error("failed to write panic log", "path", path, "error", err)
	}

	logger.Error("recovered from panic",
		"component", component,
		"reason", reason,
		"log_file", path)

	// Check crash loop
	h.mu.Lock()
	defer h.mu.Unlock()

	timestamps := h.panicCounts[component]

	// Filter old timestamps
	var recent []time.Time
	for _, t := range timestamps {
		if now.Sub(t) <= crashLoopWindow {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	h.panicCounts[component] = recent

	if len(recent) >= crashLoopMaxCount {
		return true // Disable the component
	}

	return false
}

// HandleDaemonPanic processes a daemon-level panic.
// It writes the panic stack to a file and logs it.
func HandleDaemonPanic(r interface{}, logger *slog.Logger) {
	GlobalHandler.HandlePanic("daemon", r, logger)
}
