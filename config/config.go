package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
	"github.com/priyanshu-s-rana/kv_store/models"
)

var CONFIG models.CFG

func SetConfig() {

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Config file not found: %v", err)
	}

	// Automatic env override
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.Unmarshal(&CONFIG); err != nil {
		log.Fatal("Unable to decode config")
	}
}