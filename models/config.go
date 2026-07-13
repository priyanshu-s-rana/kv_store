package models

import "time"

// CFG holds the top-level server configuration loaded from config.yaml.
type CFG struct {
	Server struct {
		Host string `mapstructure:"host"`
		Port string `mapstructure:"port"`
	} `mapstructure:"server"`
	Metrics struct {
		Host string `mapstructure:"host"`
		Port string `mapstructure:"port"`
	} `mapstructure:"metrics"`
	Persistence struct {
		Journal struct {
			Path1  string `mapstructure:"path1"`
			Path2  string `mapstructure:"path2"`
			Policy string `mapstructure:"policy"`
		} `mapstructure:"journal"`
		Snapshot struct {
			Path     string        `mapstructure:"path"`
			Interval time.Duration `mapstructure:"interval"`
		} `mapstructure:"snapshot"`
	} `mapstructure:"persistence"`
	Memory struct {
		MaxSize int64 `mapstructure:"max_size"`
	} `mapstructure:"memory"`
}
