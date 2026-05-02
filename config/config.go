package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
	"github.com/priyanshu-s-rana/kv_store/models"
)

var CONFIG models.CFG

func SetConfig() {

	// Adding config path
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Giving config file name with extension
	viper.SetConfigFile("config.yaml")

	err := viper.ReadInConfig()
	if err != nil {
		log.Println("Config file not found, using env/defaults")
	}

	// Automatic env override
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	err = viper.Unmarshal(&CONFIG)
	if err != nil {
		log.Fatal("Unable to decode config")
	}
}