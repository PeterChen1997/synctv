package vendoremby

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	json "github.com/json-iterator/go"
	"github.com/PeterChen1997/synctv/internal/cache"
	"github.com/PeterChen1997/synctv/internal/db"
	dbModel "github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/vendors/api/emby"
)

type LoginReq struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r *LoginReq) Validate() error {
	if r.Host == "" {
		return errors.New("host is required")
	}
	url, err := url.Parse(r.Host)
	if err != nil {
		return err
	}
	if url.Scheme != "http" && url.Scheme != "https" {
		return errors.New("host is invalid")
	}
	r.Host = strings.TrimRight(url.String(), "/")
	if r.Username == "" {
		return errors.New("username is required")
	}
	return nil
}

func (r *LoginReq) Decode(ctx *gin.Context) error {
	return json.NewDecoder(ctx.Request.Body).Decode(r)
}

func Login(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	req := LoginReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	backend := ctx.Query("backend")
	cli := vendor.LoadEmbyClient(backend)

	data, err := cli.Login(ctx, &emby.LoginReq{
		Host:     req.Host,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if data.GetServerId() == "" {
		ctx.AbortWithStatusJSON(
			http.StatusInternalServerError,
			model.NewAPIErrorStringResp("serverID is empty"),
		)
		return
	}

	_, err = db.CreateOrSaveEmbyVendor(&dbModel.EmbyVendor{
		UserID:     user.ID,
		ServerID:   data.GetServerId(),
		Host:       req.Host,
		APIKey:     data.GetToken(),
		Backend:    backend,
		EmbyUserID: data.GetUserId(),
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	_, err = user.EmbyCache().
		StoreOrRefreshWithDynamicFunc(ctx, data.GetServerId(), func(_ context.Context, key string) (*cache.EmbyUserCacheData, error) {
			return &cache.EmbyUserCacheData{
				Host:     req.Host,
				ServerID: key,
				APIKey:   data.GetToken(),
				Backend:  backend,
				UserID:   data.GetUserId(),
			}, nil
		})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func Logout(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	var req model.ServerIDReq
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	err := db.DeleteEmbyVendor(user.ID, req.ServerID)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	eucd, ok := user.EmbyCache().LoadCache(req.ServerID)
	if ok {
		eucdr, _ := eucd.Raw()
		go logoutEmby(eucdr)
	}

	ctx.Status(http.StatusNoContent)
}

func logoutEmby(eucd *cache.EmbyUserCacheData) {
	if eucd == nil || eucd.APIKey == "" {
		return
	}
	_, _ = vendor.LoadEmbyClient(eucd.Backend).Logout(context.Background(), &emby.LogoutReq{
		Host:  eucd.Host,
		Token: eucd.APIKey,
	})
}
