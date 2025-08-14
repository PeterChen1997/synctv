package vendorbilibili

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	json "github.com/json-iterator/go"
	"github.com/PeterChen1997/synctv/internal/cache"
	"github.com/PeterChen1997/synctv/internal/db"
	dbModel "github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/PeterChen1997/vendors/api/bilibili"
)

func NewQRCode(ctx *gin.Context) {
	r, err := vendor.LoadBilibiliClient("").NewQRCode(ctx, &bilibili.Empty{})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	ctx.JSON(http.StatusOK, model.NewAPIDataResp(r))
}

type QRCodeLoginReq struct {
	Key string `json:"key"`
}

func (r *QRCodeLoginReq) Validate() error {
	if r.Key == "" {
		return errors.New("key is empty")
	}
	return nil
}

func (r *QRCodeLoginReq) Decode(ctx *gin.Context) error {
	return json.NewDecoder(ctx.Request.Body).Decode(r)
}

func LoginWithQR(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	req := QRCodeLoginReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	backend := ctx.Query("backend")
	resp, err := vendor.LoadBilibiliClient(backend).
		LoginWithQRCode(ctx, &bilibili.LoginWithQRCodeReq{
			Key: req.Key,
		})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	switch resp.GetStatus() {
	case bilibili.QRCodeStatus_EXPIRED:
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(gin.H{
			"status": "expired",
		}))
		return
	case bilibili.QRCodeStatus_SCANNED:
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(gin.H{
			"status": "scanned",
		}))
		return
	case bilibili.QRCodeStatus_NOTSCANNED:
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(gin.H{
			"status": "notScanned",
		}))
		return
	case bilibili.QRCodeStatus_SUCCESS:
		_, err = db.CreateOrSaveBilibiliVendor(&dbModel.BilibiliVendor{
			UserID:  user.ID,
			Cookies: resp.GetCookies(),
			Backend: backend,
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		_, err = user.BilibiliCache().
			Data().
			Refresh(ctx, func(_ context.Context, _ ...struct{}) (*cache.BilibiliUserCacheData, error) {
				return &cache.BilibiliUserCacheData{
					Backend: backend,
					Cookies: utils.MapToHTTPCookie(resp.GetCookies()),
				}, nil
			})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		ctx.JSON(http.StatusOK, model.NewAPIDataResp(gin.H{
			"status": "success",
		}))
	default:
		ctx.AbortWithStatusJSON(
			http.StatusInternalServerError,
			model.NewAPIErrorStringResp("unknown status"),
		)
		return
	}
}

func NewCaptcha(ctx *gin.Context) {
	r, err := vendor.LoadBilibiliClient("").NewCaptcha(ctx, &bilibili.Empty{})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	ctx.JSON(http.StatusOK, model.NewAPIDataResp(r))
}

type SMSReq struct {
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
	V         string `json:"validate"`
	Telephone string `json:"telephone"`
}

func (r *SMSReq) Validate() error {
	if r.Token == "" {
		return errors.New("token is empty")
	}
	if r.Challenge == "" {
		return errors.New("challenge is empty")
	}
	if r.V == "" {
		return errors.New("validate is empty")
	}
	if r.Telephone == "" {
		return errors.New("telephone is empty")
	}
	return nil
}

func (r *SMSReq) Decode(ctx *gin.Context) error {
	return json.NewDecoder(ctx.Request.Body).Decode(r)
}

func NewSMS(ctx *gin.Context) {
	var req SMSReq
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	r, err := vendor.LoadBilibiliClient("").NewSMS(ctx, &bilibili.NewSMSReq{
		Phone:     req.Telephone,
		Token:     req.Token,
		Challenge: req.Challenge,
		Validate:  req.V,
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	ctx.JSON(http.StatusOK, model.NewAPIDataResp(gin.H{
		"captchaKey": r.GetCaptchaKey(),
	}))
}

type SMSLoginReq struct {
	Telephone  string `json:"telephone"`
	CaptchaKey string `json:"captchaKey"`
	Code       string `json:"code"`
}

func (r *SMSLoginReq) Validate() error {
	if r.Telephone == "" {
		return errors.New("telephone is empty")
	}
	if r.CaptchaKey == "" {
		return errors.New("captchaKey is empty")
	}
	if r.Code == "" {
		return errors.New("code is empty")
	}
	return nil
}

func (r *SMSLoginReq) Decode(ctx *gin.Context) error {
	return json.NewDecoder(ctx.Request.Body).Decode(r)
}

func LoginWithSMS(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	var req SMSLoginReq
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	backend := ctx.Query("backend")
	c, err := vendor.LoadBilibiliClient(backend).LoginWithSMS(ctx, &bilibili.LoginWithSMSReq{
		Phone:      req.Telephone,
		CaptchaKey: req.CaptchaKey,
		Code:       req.Code,
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	_, err = db.CreateOrSaveBilibiliVendor(&dbModel.BilibiliVendor{
		UserID:  user.ID,
		Backend: backend,
		Cookies: c.GetCookies(),
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	_, err = user.BilibiliCache().
		Data().
		Refresh(ctx, func(_ context.Context, _ ...struct{}) (*cache.BilibiliUserCacheData, error) {
			return &cache.BilibiliUserCacheData{
				Backend: backend,
				Cookies: utils.MapToHTTPCookie(c.GetCookies()),
			}, nil
		})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	ctx.Status(http.StatusNoContent)
}

func Logout(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)
	user := middlewares.GetUserEntry(ctx).Value()
	err := db.DeleteBilibiliVendor(user.ID)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}
	err = user.BilibiliCache().Clear(ctx)
	if err != nil {
		log.Errorf("clear bilibili cache: %v", err)
	}
	ctx.Status(http.StatusNoContent)
}
