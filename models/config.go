package models

import "time"

// CFG holds the top-level server configuration loaded from config.yaml.
type CFG struct {
	Server struct {
		Host string `mapstructure:"host"`
		Port string `mapstructure:"port"`
	} `mapstructure:"server"`
	Snapshot struct {
		Path     string        `mapstructure:"path"`
		Interval time.Duration `mapstructure:"interval"`
	} `mapstructure:"snapshot"`
	Memory struct {
		MaxSize int64 `mapstructure:"max_size"`
	} `mapstructure:"memory"`
}
