package vendorbilibili

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/PeterChen1997/synctv/internal/db"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/PeterChen1997/vendors/api/bilibili"
)

type BilibiliMeResp = model.VendorMeResp[*bilibili.UserInfoResp]

func Me(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	bucd, err := user.BilibiliCache().Get(ctx)
	if err != nil {
		if errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			ctx.JSON(http.StatusOK, model.NewAPIDataResp(&BilibiliMeResp{
				IsLogin: false,
			}))
			return
		}
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	if len(bucd.Cookies) == 0 {
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(&BilibiliMeResp{
			IsLogin: false,
		}))
		return
	}
	resp, err := vendor.LoadBilibiliClient(bucd.Backend).UserInfo(ctx, &bilibili.UserInfoReq{
		Cookies: utils.HTTPCookieToMap(bucd.Cookies),
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(&BilibiliMeResp{
		IsLogin: resp.GetIsLogin(),
		Info:    resp,
	}))
}
