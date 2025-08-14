package vendorbilibili

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	json "github.com/json-iterator/go"
	"github.com/PeterChen1997/synctv/internal/db"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/PeterChen1997/vendors/api/bilibili"
)

type ParseReq struct {
	URL string `json:"url"`
}

func (r *ParseReq) Validate() error {
	if r.URL == "" {
		return errors.New("url is empty")
	}
	return nil
}

func (r *ParseReq) Decode(ctx *gin.Context) error {
	return json.NewDecoder(ctx.Request.Body).Decode(r)
}

func Parse(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	req := ParseReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	cli := vendor.LoadBilibiliClient(ctx.Query("backend"))

	resp, err := cli.Match(ctx, &bilibili.MatchReq{
		Url: req.URL,
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	// can be no login
	var cookies []*http.Cookie
	bucd, err := user.BilibiliCache().Get(ctx)
	if err != nil {
		if !errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
	} else {
		cookies = bucd.Cookies
	}

	switch resp.GetType() {
	case "bv":
		resp, err := cli.ParseVideoPage(ctx, &bilibili.ParseVideoPageReq{
			Cookies:  utils.HTTPCookieToMap(cookies),
			Bvid:     resp.GetId(),
			Sections: ctx.DefaultQuery("sections", "false") == "true",
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
	case "av":
		aid, err := strconv.ParseUint(resp.GetId(), 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		resp, err := cli.ParseVideoPage(ctx, &bilibili.ParseVideoPageReq{
			Cookies:  utils.HTTPCookieToMap(cookies),
			Aid:      aid,
			Sections: ctx.DefaultQuery("sections", "false") == "true",
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
	case "ep":
		epid, err := strconv.ParseUint(resp.GetId(), 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		resp, err := cli.ParsePGCPage(ctx, &bilibili.ParsePGCPageReq{
			Cookies: utils.HTTPCookieToMap(cookies),
			Epid:    epid,
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
	case "ss":
		ssid, err := strconv.ParseUint(resp.GetId(), 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		resp, err := cli.ParsePGCPage(ctx, &bilibili.ParsePGCPageReq{
			Cookies: utils.HTTPCookieToMap(cookies),
			Ssid:    ssid,
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
	case "live":
		roomid, err := strconv.ParseUint(resp.GetId(), 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		resp, err := cli.ParseLivePage(ctx, &bilibili.ParseLivePageReq{
			Cookies: utils.HTTPCookieToMap(cookies),
			RoomID:  roomid,
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
	default:
		ctx.AbortWithStatusJSON(
			http.StatusInternalServerError,
			model.NewAPIErrorStringResp("unknown match type "+resp.GetType()),
		)
		return
	}
}
