package vendoralist

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	json "github.com/json-iterator/go"
	"github.com/PeterChen1997/synctv/internal/db"
	dbModel "github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/PeterChen1997/vendors/api/alist"
	"gorm.io/gorm"
)

type ListReq struct {
	Path     string `json:"path"`
	Password string `json:"password"`
	Keyword  string `json:"keyword"`
	Refresh  bool   `json:"refresh"`
}

func (r *ListReq) Validate() (err error) {
	return nil
}

func (r *ListReq) Decode(ctx *gin.Context) error {
	return json.NewDecoder(ctx.Request.Body).Decode(r)
}

type AlistFileItem struct {
	*model.Item
	Size uint64 `json:"size"`
}

type AlistFSListResp = model.VendorFSListResp[*AlistFileItem]

//nolint:gosec
func List(ctx *gin.Context) {
	user := middlewares.GetUserEntry(ctx).Value()

	req := ListReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	page, size, err := utils.GetPageAndMax(ctx)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if req.Path == "" {
		socpes := [](func(*gorm.DB) *gorm.DB){
			db.OrderByCreatedAtAsc,
		}

		total, err := db.GetAlistVendorsCount(user.ID, socpes...)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		if total == 0 {
			ctx.JSON(http.StatusBadRequest, model.NewAPIErrorStringResp("alist server not found"))
			return
		}

		ev, err := db.GetAlistVendors(user.ID, append(socpes, db.Paginate(page, size))...)
		if err != nil {
			if errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
				ctx.JSON(
					http.StatusBadRequest,
					model.NewAPIErrorStringResp("alist server not found"),
				)
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}

		if total == 1 {
			req.Path = ev[0].ServerID + "/"
			goto AlistFSListResp
		}

		resp := AlistFSListResp{
			Paths: []*model.Path{
				{
					Name: "",
					Path: "",
				},
			},
			Total: uint64(total),
		}

		for _, evi := range ev {
			resp.Items = append(resp.Items, &AlistFileItem{
				Item: &model.Item{
					Name:  evi.Host,
					Path:  evi.ServerID + `/`,
					IsDir: true,
				},
			})
		}

		ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))

		return
	}

AlistFSListResp:

	var serverID string
	serverID, req.Path, err = dbModel.GetAlistServerIDFromPath(req.Path)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if !strings.HasPrefix(req.Path, "/") {
		req.Path = "/" + req.Path
	}

	aucd, err := user.AlistCache().LoadOrStore(ctx, serverID)
	if err != nil {
		if errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			ctx.JSON(http.StatusBadRequest, model.NewAPIErrorStringResp("alist server not found"))
			return
		}

		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	cli := vendor.LoadAlistClient(ctx.Query("backend"))
	if req.Keyword != "" {
		data, err := cli.FsSearch(ctx, &alist.FsSearchReq{
			Token:    aucd.Token,
			Password: req.Password,
			Parent:   req.Path,
			Keywords: req.Keyword,
			Host:     aucd.Host,
			Page:     uint64(page),
			PerPage:  uint64(size),
		})
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}

		req.Path = strings.Trim(req.Path, "/")
		resp := AlistFSListResp{
			Total: data.GetTotal(),
			Paths: model.GenDefaultPaths(req.Path, true,
				&model.Path{
					Name: "",
					Path: "",
				},
				&model.Path{
					Name: aucd.Host,
					Path: aucd.ServerID + "/",
				}),
		}
		for _, flr := range data.GetContent() {
			resp.Items = append(resp.Items, &AlistFileItem{
				Item: &model.Item{
					Name: flr.GetName(),
					Path: fmt.Sprintf(
						"%s/%s",
						aucd.ServerID,
						strings.Trim(fmt.Sprintf("%s/%s", flr.GetParent(), flr.GetName()), "/"),
					),
					IsDir: flr.GetIsDir(),
				},
				Size: flr.GetSize(),
			})
		}

		ctx.JSON(http.StatusOK, model.NewAPIDataResp(&resp))
		return
	}

	data, err := cli.FsList(ctx, &alist.FsListReq{
		Token:    aucd.Token,
		Password: req.Password,
		Path:     req.Path,
		Host:     aucd.Host,
		Refresh:  req.Refresh,
		Page:     uint64(page),
		PerPage:  uint64(size),
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	req.Path = strings.Trim(req.Path, "/")
	resp := AlistFSListResp{
		Total: data.GetTotal(),
		Paths: model.GenDefaultPaths(req.Path, true,
			&model.Path{
				Name: "",
				Path: "",
			},
			&model.Path{
				Name: aucd.Host,
				Path: aucd.ServerID + "/",
			}),
	}
	for _, flr := range data.GetContent() {
		resp.Items = append(resp.Items, &AlistFileItem{
			Item: &model.Item{
				Name: flr.GetName(),
				Path: fmt.Sprintf(
					"%s/%s",
					aucd.ServerID,
					strings.Trim(fmt.Sprintf("%s/%s", req.Path, flr.GetName()), "/"),
				),
				IsDir: flr.GetIsDir(),
			},
			Size: flr.GetSize(),
		})
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(&resp))
}
