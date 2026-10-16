// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// MemoryBackupService periodically archives the workspace memory directory to
// the configured backup directory. It is a no-op when disabled.
type MemoryBackupService struct {
	workspace  string
	configPath string
	dir        string
	keep       int
	interval   time.Duration
	enabled    bool
	cancel     context.CancelFunc
}

// NewMemoryBackupService creates the scheduled-backup service. A non-positive
// interval falls back to 24h.
func NewMemoryBackupService(workspace, configPath, dir string, enabled bool, interval time.Duration, keep int) *MemoryBackupService {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &MemoryBackupService{
		workspace:  workspace,
		configPath: configPath,
		dir:        dir,
		keep:       keep,
		interval:   interval,
		enabled:    enabled,
	}
}

// Start launches the ticker goroutine. It does nothing when disabled.
func (s *MemoryBackupService) Start() error {
	if s == nil || !s.enabled {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.loop(ctx)
	return nil
}

// Stop halts the ticker.
func (s *MemoryBackupService) Stop() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

func (s *MemoryBackupService) loop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			path, err := CreateMemoryArchive(s.workspace, s.dir, s.configPath, false, s.keep)
			if err != nil {
				logger.WarnCF("agent", "scheduled memory backup failed", map[string]any{"error": err.Error()})
				continue
			}
			logger.InfoCF("agent", "scheduled memory backup written", map[string]any{"path": path})
		}
	}
}
