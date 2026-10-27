package ingest

import (
	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/domain"
)

func cachePrefix(s domain.Sport) string { return application.SportCachePrefix(s) }
