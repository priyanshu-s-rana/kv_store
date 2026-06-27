package config

import (
	"log"
	"strings"

	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/spf13/viper"
)

var CONFIG models.CFG

func SetConfig(env string) {
	viper.SetConfigName("config." + env)
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
	log.Printf("[config] loaded config.%s.yaml", env)
}
