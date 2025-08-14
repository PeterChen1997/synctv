package bootstrap

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	sysnotify "github.com/PeterChen1997/synctv/internal/sysnotify"
	"github.com/PeterChen1997/synctv/internal/version"
)

func InitCheckUpdate(ctx context.Context) error {
	v, err := version.NewVersionInfo()
	if err != nil {
		log.Fatalf("get version info error: %v", err)
	}

	go func() {
		execFile, err := version.ExecutableFile()
		if err != nil {
			execFile = "synctv"
		}

		var (
			need   bool
			latest string
			url    string
		)
		need, latest, url, err = check(ctx, v)
		if err != nil {
			log.Errorf("check update error: %v", err)
		} else if need {
			log.Infof("new version (%s) available: %s", latest, url)
			log.Infof("run '%s self-update' to auto update", execFile)
		}

		err = sysnotify.RegisterSysNotifyTask(0, sysnotify.NewSysNotifyTask(
			"check-update",
			sysnotify.NotifyTypeEXIT,
			func() error {
				if need {
					log.Infof("new version (%s) available: %s", latest, url)
					log.Infof("run '%s self-update' to auto update", execFile)
				}
				return nil
			},
		))
		if err != nil {
			log.Errorf("register sys notify task error: %v", err)
		}

		t := time.NewTicker(time.Hour * 6)
		defer t.Stop()
		for range t.C {
			func() {
				defer func() {
					if err := recover(); err != nil {
						log.Errorf("check update panic: %v", err)
					}
				}()
				need, latest, url, err = check(ctx, v)
				if err != nil {
					log.Errorf("check update error: %v", err)
				}
			}()
		}
	}()

	return nil
}

func check(ctx context.Context, v *version.Info) (need bool, latest, url string, err error) {
	l, err := v.CheckLatest(ctx)
	if err != nil {
		return false, "", "", err
	}
	latest = l
	b, err := v.NeedUpdate(ctx)
	if err != nil {
		return false, "", "", err
	}
	need = b
	if b {
		u, err := v.LatestBinaryURL(ctx)
		if err != nil {
			return false, "", "", err
		}
		url = u
	}
	return need, latest, url, nil
}
