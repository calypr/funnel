package plugin

import (
	"time"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
)

const taskPluginTimeout = 60 * time.Second

// Authorize is implemented by Funnel authorization plugins.
type Authorize interface {
	PluginAction(params map[string]string, headers map[string]*StringList, config *config.Config, task *tes.Task, actionType Type) (*JobResponse, error)
}
