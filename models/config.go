package models

import "time"

type CFG struct {
	Server struct {
		Host string `mapstructure:"host"`
		Port string `mapstructure:"port"`
	} `mapstructure:"server"`
	Snapshot struct {
		Path     string        `mapstructure:"path"`
		Interval time.Duration `mapstructure:"interval"`
	} `mapstructure:"snapshot"`
}
