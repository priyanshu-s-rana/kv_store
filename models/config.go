package models

type CFG struct {
	Server struct {
		Port int `mapstructure:"port"`
	} `mapstructure:"server"`
	Snapshot struct {
		Path     string `mapstructure:"path"`
		Interval int 	`mapstructure:"interval"`
	} `mapstructure:"snapshot"`
}
