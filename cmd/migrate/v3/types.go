package v3

import (
	databaseconfig "github.com/dexilon-exchange/v3/database/config"
	loggingconfig "github.com/dexilon-exchange/v3/logging/config"
	"github.com/dexilon-exchange/v3/modules/pruning"
	"github.com/dexilon-exchange/v3/modules/telemetry"
	nodeconfig "github.com/dexilon-exchange/v3/node/config"
	parserconfig "github.com/dexilon-exchange/v3/parser/config"
	pricefeedconfig "github.com/dexilon-exchange/v3/pricefeed"
	"github.com/dexilon-exchange/v3/types/config"
)

// Config defines all necessary juno configuration parameters.
type Config struct {
	Chain    config.ChainConfig    `yaml:"chain"`
	Node     nodeconfig.Config     `yaml:"node"`
	Parser   parserconfig.Config   `yaml:"parsing"`
	Database databaseconfig.Config `yaml:"database"`
	Logging  loggingconfig.Config  `yaml:"logging"`

	// The following are there to support modules which config are present if they are enabled

	Telemetry *telemetry.Config       `yaml:"telemetry,omitempty"`
	Pruning   *pruning.Config         `yaml:"pruning,omitempty"`
	PriceFeed *pricefeedconfig.Config `yaml:"pricefeed,omitempty"`
}
