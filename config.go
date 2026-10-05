package main

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

var version = "dev" // set by `make build`

// config is plugins.configs.paced-affinity; CPA itself handles enabled and priority.
type config struct {
	Shadow    bool     `yaml:"shadow"`    // decide and log, but let the built-in selector route
	Providers []string `yaml:"providers"` // providers this plugin routes
}

func parseConfig(raw []byte) (*config, error) {
	var req struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	cfg := &config{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	if err := yaml.Unmarshal(req.ConfigYAML, cfg); err != nil {
		return nil, err
	}
	if len(cfg.Providers) == 0 {
		cfg.Providers = []string{"claude", "codex"}
	}
	for i, p := range cfg.Providers {
		cfg.Providers[i] = strings.ToLower(strings.TrimSpace(p))
	}
	return cfg, nil
}

func (c *config) routes(provider string) bool {
	_, known := profiles[provider]
	return known && slices.Contains(c.Providers, provider)
}

func registration() map[string]any {
	return map[string]any{
		"schema_version": pluginabi.SchemaVersion,
		"metadata": pluginapi.Metadata{
			Name:             "paced-affinity",
			Version:          version,
			Author:           "Pandoks",
			GitHubRepository: "https://github.com/Pandoks/cpa-plugin-paced-affinity",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "shadow", Type: pluginapi.ConfigFieldTypeBoolean,
					Description: "Compute and log decisions at debug level, but let the built-in selector route."},
				{Name: "providers", Type: pluginapi.ConfigFieldTypeArray,
					Description: "Providers to route (default [claude, codex]); others go to the built-in selector."},
			},
		},
		"capabilities": map[string]bool{"scheduler": true, "usage_plugin": true, "management_api": true},
	}
}
