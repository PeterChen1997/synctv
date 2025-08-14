package conf

import (
	"github.com/PeterChen1997/synctv/utils"
)

type JwtConfig struct {
	Secret string `env:"JWT_SECRET" yaml:"secret"`
	Expire string `env:"JWT_EXPIRE" yaml:"expire"`
}

func DefaultJwtConfig() JwtConfig {
	return JwtConfig{
		Secret: utils.RandString(32),
		Expire: "48h",
	}
}
