package bootstrap

import (
	"context"

	sysnotify "github.com/PeterChen1997/synctv/internal/sysnotify"
)

func InitSysNotify(_ context.Context) error {
	sysnotify.Init()
	return nil
}
