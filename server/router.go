package server

import (
	"github.com/gin-gonic/gin"
	"github.com/PeterChen1997/synctv/cmd/flags"
	"github.com/PeterChen1997/synctv/server/handlers"
	"github.com/PeterChen1997/synctv/server/middlewares"
	auth "github.com/PeterChen1997/synctv/server/oauth2"
	"github.com/PeterChen1997/synctv/server/static"
)

func Init(e *gin.Engine) {
	middlewares.Init(e)
	auth.Init(e)
	handlers.Init(e)
	if !flags.Server.DisableWeb {
		static.Init(e)
	}
}

func NewAndInit() (e *gin.Engine) {
	e = gin.New()
	Init(e)
	return
}
