package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/PeterChen1997/synctv/internal/conf"
	dbModel "github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/op"
	"github.com/PeterChen1997/synctv/internal/rtmp"
	"github.com/PeterChen1997/synctv/internal/settings"
	"github.com/PeterChen1997/synctv/server/handlers/proxy"
	"github.com/PeterChen1997/synctv/server/handlers/vendors"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/zijiren233/livelib/protocol/hls"
	"github.com/zijiren233/livelib/protocol/httpflv"
)

func GetPageItems[T any](ctx *gin.Context, items []T) ([]T, error) {
	page, _max, err := utils.GetPageAndMax(ctx)
	if err != nil {
		return nil, err
	}

	return utils.GetPageItems(items, page, _max), nil
}

func genMovieInfo(
	ctx context.Context,
	room *op.Room,
	user *op.User,
	opMovie *op.Movie,
	userAgent,
	userToken string,
) (*model.Movie, error) {
	if opMovie == nil || opMovie.ID == "" {
		return &model.Movie{}, nil
	}
	if opMovie.IsFolder {
		if !opMovie.IsDynamicFolder() {
			return nil, errors.New("movie is static folder, can't get movie info")
		}
	}
	movie := opMovie.Clone()
	if movie.Type == "" && movie.URL != "" {
		movie.Type = utils.GetURLExtension(movie.URL)
	}
	switch {
	case movie.VendorInfo.Vendor != "":
		vendor, err := vendors.NewVendorService(room, opMovie)
		if err != nil {
			return nil, err
		}
		movie, err = vendor.GenMovieInfo(ctx, user, userAgent, userToken)
		if err != nil {
			return nil, err
		}
	case movie.RtmpSource:
		movie.URL = fmt.Sprintf(
			"/api/room/movie/live/hls/list/%s.m3u8?token=%s&roomId=%s",
			movie.ID,
			userToken,
			opMovie.RoomID,
		)
		movie.Type = "m3u8"
		movie.MoreSources = append(movie.MoreSources, &dbModel.MoreSource{
			Name: "flv",
			URL: fmt.Sprintf(
				"/api/room/movie/live/flv/%s.flv?token=%s&roomId=%s",
				movie.ID,
				userToken,
				opMovie.RoomID,
			),
			Type: "flv",
		})
		movie.Headers = nil
	case movie.Live && movie.Proxy:
		if !utils.IsM3u8Url(movie.URL) {
			movie.MoreSources = append(movie.MoreSources, &dbModel.MoreSource{
				Name: "flv",
				URL: fmt.Sprintf(
					"/api/room/movie/live/flv/%s.flv?token=%s&roomId=%s",
					movie.ID,
					userToken,
					opMovie.RoomID,
				),
				Type: "flv",
			})
		}
		movie.URL = fmt.Sprintf(
			"/api/room/movie/live/hls/list/%s.m3u8?token=%s&roomId=%s",
			movie.ID,
			userToken,
			opMovie.RoomID,
		)
		movie.Type = "m3u8"
		movie.Headers = nil
	case movie.Proxy:
		movie.URL = fmt.Sprintf(
			"/api/room/movie/proxy/%s?token=%s&roomId=%s",
			movie.ID,
			userToken,
			opMovie.RoomID,
		)
		movie.Headers = nil
	}
	if movie.Type == "" && movie.URL != "" {
		movie.Type = utils.GetURLExtension(movie.URL)
	}
	for _, v := range movie.MoreSources {
		if v.Type == "" {
			v.Type = utils.GetURLExtension(v.URL)
		}
	}
	for _, v := range movie.Subtitles {
		if v.Type == "" {
			v.Type = utils.GetURLExtension(v.URL)
		}
	}
	resp := &model.Movie{
		ID:        movie.ID,
		CreatedAt: movie.CreatedAt.UnixMilli(),
		Base:      movie.MovieBase,
		Creator:   op.GetUserName(movie.CreatorID),
		CreatorID: movie.CreatorID,
		SubPath:   opMovie.SubPath(),
	}
	return resp, nil
}

func genCurrentRespWithCurrent(
	ctx context.Context,
	room *op.Room,
	user *op.User,
	userAgent, userToken string,
) (*model.CurrentMovieResp, error) {
	current := room.Current()
	if current.Movie.ID == "" {
		return &model.CurrentMovieResp{
			Movie: &model.Movie{},
		}, nil
	}
	opMovie, err := room.GetMovieByID(current.Movie.ID)
	if err != nil {
		return nil, fmt.Errorf("get current movie error: %w", err)
	}
	mr, err := genMovieInfo(ctx, room, user, opMovie, userAgent, userToken)
	if err != nil {
		return nil, fmt.Errorf("gen current movie info error: %w", err)
	}
	expireID, err := opMovie.ExpireID(ctx)
	if err != nil {
		return nil, fmt.Errorf("get expire id error: %w", err)
	}
	resp := &model.CurrentMovieResp{
		Status:   current.UpdateStatus(),
		Movie:    mr,
		ExpireID: expireID,
	}
	return resp, nil
}

func CurrentMovie(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	currentResp, err := genCurrentRespWithCurrent(
		ctx,
		room,
		user,
		ctx.GetHeader("User-Agent"),
		ctx.GetString("token"),
	)
	if err != nil {
		log.Errorf("gen current resp error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(currentResp))
}

func Movies(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	if !user.HasRoomPermission(room, dbModel.PermissionGetMovieList) {
		ctx.AbortWithStatusJSON(
			http.StatusForbidden,
			model.NewAPIErrorResp(dbModel.ErrNoPermission),
		)
		return
	}

	id := ctx.Query("id")
	if len(id) != 0 && len(id) != 32 {
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("id length must be 0 or 32"),
		)
		return
	}

	page, _max, err := utils.GetPageAndMax(ctx)
	if err != nil {
		log.Errorf("get page and max error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if id != "" {
		mv, err := room.GetMovieByID(id)
		if err != nil {
			log.Errorf("get room movie by id error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		if !mv.IsFolder {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				model.NewAPIErrorStringResp("parent id is not folder"),
			)
			return
		}
		if mv.IsDynamicFolder() {
			resp, err := listVendorDynamicMovie(
				ctx,
				user,
				room,
				mv,
				ctx.Query("subPath"),
				ctx.Query("keyword"),
				page,
				_max,
			)
			if err != nil {
				log.Errorf("vendor dynamic movie list error: %v", err)
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
				return
			}
			ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
			return
		}
	}

	m, total, err := user.GetRoomMoviesWithPage(room, ctx.Query("keyword"), page, _max, id)
	if err != nil {
		log.Errorf("get room movies with page error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	paths, err := getParentMoviePath(room, id)
	if err != nil {
		log.Errorf("get parent movie path error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	resp := &model.MoviesResp{
		MovieList: &model.MovieList{
			Total:  total,
			Movies: make([]*model.Movie, len(m)),
			Paths:  paths,
		},
	}

	for i, v := range m {
		resp.Movies[i] = &model.Movie{
			ID:        v.ID,
			CreatedAt: v.CreatedAt.UnixMilli(),
			Base:      v.MovieBase,
			Creator:   op.GetUserName(v.CreatorID),
			CreatorID: v.CreatorID,
		}
		// hide url and headers when proxy
		if user.ID != v.CreatorID && v.Proxy {
			resp.Movies[i].Base.URL = ""
			resp.Movies[i].Base.Headers = nil
		}
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(resp))
}

func getParentMoviePath(room *op.Room, id string) ([]*model.MoviePath, error) {
	paths := []*model.MoviePath{}
	for id != "" {
		p, err := room.GetMovieByID(id)
		if err != nil {
			return nil, fmt.Errorf("get movie by id error: %w", err)
		}
		paths = append(paths, &model.MoviePath{
			Name: p.Name,
			ID:   p.ID,
		})
		id = p.ParentID.String()
	}
	paths = append(paths, &model.MoviePath{
		Name: "Home",
		ID:   "",
	})
	slices.Reverse(paths)
	return paths, nil
}

func listVendorDynamicMovie(
	ctx context.Context,
	reqUser *op.User,
	room *op.Room,
	movie *op.Movie,
	subPath, keyword string,
	page, _max int,
) (*model.MoviesResp, error) {
	if reqUser.ID != movie.CreatorID {
		return nil, fmt.Errorf("list vendor dynamic folder error: %w", dbModel.ErrNoPermission)
	}

	paths, err := getParentMoviePath(room, movie.ID)
	if err != nil {
		return nil, fmt.Errorf("get parent movie path error: %w", err)
	}
	vendor, err := vendors.NewVendorService(room, movie)
	if err != nil {
		return nil, err
	}
	dynamic, err := vendor.ListDynamicMovie(ctx, reqUser, subPath, keyword, page, _max)
	if err != nil {
		return nil, err
	}
	dynamic.Paths = append(paths, dynamic.Paths...)
	resp := &model.MoviesResp{
		MovieList: dynamic,
		Dynamic:   true,
	}
	return resp, nil
}

func PushMovie(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	req := model.PushMovieReq{}
	if err := model.Decode(ctx, &req); err != nil {
		log.Errorf("push movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	m, err := user.AddRoomMovie(room, (*dbModel.MovieBase)(&req))
	if err != nil {
		log.Errorf("push movie error: %v", err)
		if errors.Is(err, dbModel.ErrNoPermission) {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				model.NewAPIErrorResp(
					fmt.Errorf("push movie error: %w", err),
				),
			)
			return
		}
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(m))
}

func PushMovies(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	req := model.PushMoviesReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ms := make([]*dbModel.MovieBase, len(req))

	for i, v := range req {
		ms[i] = (*dbModel.MovieBase)(v)
	}

	m, err := user.AddRoomMovies(room, ms)
	if err != nil {
		log.Errorf("push movies error: %v", err)
		if errors.Is(err, dbModel.ErrNoPermission) {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				model.NewAPIErrorResp(
					fmt.Errorf("push movies error: %w", err),
				),
			)
			return
		}
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(m))
}

func NewPublishKey(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)

	if !conf.Conf.Server.RTMP.Enable {
		log.Errorf("rtmp is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("rtmp is not enabled"),
		)
		return
	}

	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()

	req := model.IDReq{}
	if err := model.Decode(ctx, &req); err != nil {
		log.Errorf("new publish key error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}
	movie, err := room.GetMovieByID(req.ID)
	if err != nil {
		log.Errorf("new publish key error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if movie.CreatorID != user.ID {
		log.Errorf("new publish key error: %v", dbModel.ErrNoPermission)
		ctx.AbortWithStatusJSON(
			http.StatusForbidden,
			model.NewAPIErrorResp(
				fmt.Errorf("new publish key error: %w", dbModel.ErrNoPermission),
			),
		)
		return
	}

	if !movie.RtmpSource {
		log.Errorf("new publish key error: %v", "only rtmp source movie can get publish key")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("only live movie can get publish key"),
		)
		return
	}

	token, err := rtmp.NewRtmpAuthorization(movie.ID)
	if err != nil {
		log.Errorf("new publish key error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
		return
	}

	host := settings.CustomPublishHost.Get()
	if host == "" {
		u, err := url.Parse(settings.HOST.Get())
		if err != nil {
			log.Errorf("new publish key error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		host = u.Host
	}
	if host == "" {
		host = ctx.Request.Host
	}

	ctx.JSON(http.StatusOK, model.NewAPIDataResp(gin.H{
		"host":  host,
		"app":   room.ID,
		"token": token,
	}))
}

func EditMovie(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	req := model.EditMovieReq{}
	if err := model.Decode(ctx, &req); err != nil {
		log.Errorf("edit movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if err := user.UpdateRoomMovie(room, req.ID, (*dbModel.MovieBase)(&req.PushMovieReq)); err != nil {
		log.Errorf("edit movie error: %v", err)
		if errors.Is(err, dbModel.ErrNoPermission) {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				model.NewAPIErrorResp(
					fmt.Errorf("edit movie error: %w", err),
				),
			)
			return
		}
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func DelMovie(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	req := model.IDsReq{}
	if err := model.Decode(ctx, &req); err != nil {
		log.Errorf("del movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	err := user.DeleteRoomMoviesByID(room, req.IDs)
	if err != nil {
		log.Errorf("del movie error: %v", err)
		if errors.Is(err, dbModel.ErrNoPermission) {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				model.NewAPIErrorResp(
					fmt.Errorf("del movie error: %w", err),
				),
			)
			return
		}
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func ClearMovies(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()

	var req model.ClearMoviesReq
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if err := user.ClearRoomMoviesByParentID(room, req.ParentID); err != nil {
		if errors.Is(err, dbModel.ErrNoPermission) {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				model.NewAPIErrorResp(
					fmt.Errorf("clear movies error: %w", err),
				),
			)
			return
		}
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func SwapMovie(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()

	req := model.SwapMovieReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if err := user.SwapRoomMoviePositions(room, req.ID1, req.ID2); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func ChangeCurrentMovie(ctx *gin.Context) {
	room := middlewares.GetRoomEntry(ctx).Value()
	user := middlewares.GetUserEntry(ctx).Value()
	log := middlewares.GetLogger(ctx)

	req := model.SetRoomCurrentMovieReq{}
	err := model.Decode(ctx, &req)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	err = user.SetRoomCurrentMovie(room, req.ID, req.SubPath, true)
	if err != nil {
		log.Errorf("change current movie error: %v", err)
		if errors.Is(err, dbModel.ErrNoPermission) {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				model.NewAPIErrorResp(
					fmt.Errorf("change current movie error: %w", err),
				),
			)
			return
		}
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func ProxyMovie(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)

	room := middlewares.GetRoomEntry(ctx).Value()
	// user := middlewares.GetUserEntry(ctx).Value()

	m, err := room.GetMovieByID(ctx.Param("movieId"))
	if err != nil {
		log.Errorf("get movie by id error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if m.VendorInfo.Vendor != "" {
		vendor, err := vendors.NewVendorService(room, m)
		if err != nil {
			log.Errorf("get vendor service error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
			return
		}
		vendor.ProxyMovie(ctx)
		return
	}

	if !settings.MovieProxy.Get() {
		log.Errorf("proxy is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("proxy is not enabled"),
		)
		return
	}

	if !m.Proxy {
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("movie is not proxy"),
		)
		return
	}

	if m.Live || m.RtmpSource {
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp(
				"this movie is live or rtmp source, not support use this method proxy",
			),
		)
		return
	}

	switch m.Type {
	case "mpd":
		// TODO: cache mpd file
		fallthrough
	default:
		err = proxy.AutoProxyURL(ctx,
			m.URL,
			m.Type,
			m.Headers,
			ctx.GetString("token"),
			room.ID,
			m.ID,
			proxy.WithProxyURLCache(true),
		)
		if err != nil {
			log.Errorf("proxy movie error: %v", err)
			return
		}
	}
}

func ServeM3u8(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)

	if !settings.MovieProxy.Get() {
		log.Errorf("movie proxy is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("movie proxy is not enabled"),
		)
		return
	}

	room := middlewares.GetRoomEntry(ctx).Value()

	m, err := room.GetMovieByID(ctx.Param("movieId"))
	if err != nil {
		log.Errorf("get movie by id error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}

	if m.RtmpSource {
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp(
				"this movie is rtmp source, not support use this method proxy",
			),
		)
		return
	}

	if !m.Proxy {
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("movie is not proxy"),
		)
		return
	}

	targetToken := ctx.Param("targetToken")
	claims, err := proxy.GetM3u8Target(targetToken)
	if err != nil {
		log.Errorf("auth m3u8 error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorResp(err))
		return
	}
	if claims.RoomID != room.ID || claims.MovieID != m.ID {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp("invalid token"))
		return
	}
	err = proxy.M3u8(ctx,
		claims.TargetURL,
		m.Headers,
		claims.IsM3u8File,
		ctx.GetString("token"),
		room.ID,
		m.ID,
		proxy.WithProxyURLCache(true),
	)
	if err != nil {
		log.Errorf("proxy m3u8 error: %v", err)
	}
}

// only cache mpd file
// func initDashCache(ctx context.Context, movie *dbModel.Movie) func() (any, error) {
// 	return func() (any, error) {
// 		req, err := http.NewRequestWithContext(ctx, http.MethodGet, movie.Base.Url, nil)
// 		if err != nil {
// 			return nil, err
// 		}
// 		for k, v := range movie.Base.Headers {
// 			req.Header.Set(k, v)
// 		}
// 		req.Header.Set("User-Agent", utils.UA)
// 		resp, err := uhc.Do(req)
// 		if err != nil {
// 			return nil, err
// 		}
// 		defer resp.Body.Close()
// 		b, err := io.ReadAll(resp.Body)
// 		if err != nil {
// 			return nil, err
// 		}
// 		m, err := mpd.ReadFromString(string(b))
// 		if err != nil {
// 			return nil, err
// 		}
// 		if len(m.BaseURL) != 0 && !path.IsAbs(m.BaseURL[0]) {
// 			result, err := url.JoinPath(path.Dir(movie.Base.Url), m.BaseURL[0])
// 			if err != nil {
// 				return nil, err
// 			}
// 			m.BaseURL = []string{result}
// 		}
// 		s, err := m.WriteToString()
// 		if err != nil {
// 			return nil, err
// 		}
// 		return s, nil
// 	}
// }

type FormatNotSupportFileTypeError string

func (e FormatNotSupportFileTypeError) Error() string {
	return "not support file type " + string(e)
}

func JoinFlvLive(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)

	ctx.Header("Cache-Control", "no-store")
	room := middlewares.GetRoomEntry(ctx).Value()
	movieID := strings.TrimSuffix(strings.Trim(ctx.Param("movieId"), "/"), ".flv")
	m, err := room.GetMovieByID(movieID)
	if err != nil {
		log.Errorf("join flv live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}
	if !m.Live {
		log.Error("join hls live error: live is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("live is not enabled"),
		)
		return
	}
	if m.RtmpSource {
		if !conf.Conf.Server.RTMP.Enable {
			log.Error("join hls live error: rtmp is not enabled")
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				model.NewAPIErrorStringResp("rtmp is not enabled"),
			)
			return
		}
	} else if !settings.LiveProxy.Get() {
		log.Error("join hls live error: live proxy is not enabled")
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp("live proxy is not enabled"))
		return
	}
	channel, err := m.Channel()
	if err != nil {
		log.Errorf("join flv live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}

	w := httpflv.NewHttpFLVWriter(ctx.Writer)
	defer w.Close()
	err = channel.AddPlayer(w)
	if err != nil {
		log.Errorf("join flv live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}
	err = w.SendPacket(ctx.Request.Context())
	if err != nil {
		log.Errorf("join flv live error: %v", err)
		return
	}
}

func JoinHlsLive(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)

	ctx.Header("Cache-Control", "no-store")
	room := middlewares.GetRoomEntry(ctx).Value()
	movieID := strings.TrimSuffix(strings.Trim(ctx.Param("movieId"), "/"), ".m3u8")
	m, err := room.GetMovieByID(movieID)
	if err != nil {
		log.Errorf("join hls live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}
	if !m.Live {
		log.Error("join hls live error: live is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("live is not enabled"),
		)
		return
	}
	if m.RtmpSource {
		if !conf.Conf.Server.RTMP.Enable {
			log.Error("join hls live error: rtmp is not enabled")
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				model.NewAPIErrorStringResp("rtmp is not enabled"),
			)
			return
		}
	} else if !settings.LiveProxy.Get() {
		log.Error("join hls live error: live proxy is not enabled")
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp("live proxy is not enabled"))
		return
	}

	if utils.IsM3u8Url(m.URL) {
		err = proxy.M3u8(ctx,
			m.URL,
			m.Headers,
			true,
			ctx.GetString("token"),
			room.ID,
			m.ID,
			proxy.WithProxyURLCache(true),
		)
		if err != nil {
			log.Errorf("proxy m3u8 hls live error: %v", err)
		}
		return
	}
	channel, err := m.Channel()
	if err != nil {
		log.Errorf("join hls live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}

	b, err := channel.GenM3U8File(func(tsName string) (tsPath string) {
		ext := "ts"
		if settings.TSDisguisedAsPng.Get() {
			ext = "png"
		}
		return fmt.Sprintf(
			"/api/room/movie/live/hls/data/%s/%s/%s.%s",
			room.ID,
			movieID,
			tsName,
			ext,
		)
	})
	if err != nil {
		log.Errorf("join hls live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}
	ctx.Data(http.StatusOK, hls.M3U8ContentType, b)
}

//nolint:gosec
func ServeHlsLive(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)
	roomID := ctx.Param("roomId")
	roomE, err := op.LoadRoomByID(roomID)
	if err != nil {
		log.Errorf("serve hls live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}
	room := roomE.Value()

	ctx.Header("Cache-Control", "public, max-age=30, s-maxage=90")

	movieID := ctx.Param("movieId")
	m, err := room.GetMovieByID(movieID)
	if err != nil {
		log.Errorf("serve hls live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}
	if !m.Live {
		log.Error("join hls live error: live is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("live is not enabled"),
		)
		return
	}
	if m.RtmpSource {
		if !conf.Conf.Server.RTMP.Enable {
			log.Error("join hls live error: rtmp is not enabled")
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				model.NewAPIErrorStringResp("rtmp is not enabled"),
			)
			return
		}
	} else if !settings.LiveProxy.Get() {
		log.Error("join hls live error: live proxy is not enabled")
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp("live proxy is not enabled"))
		return
	}
	channel, err := m.Channel()
	if err != nil {
		log.Errorf("serve hls live error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
		return
	}

	dataID := ctx.Param("dataId")
	switch fileExt := filepath.Ext(dataID); fileExt {
	case ".ts":
		if settings.TSDisguisedAsPng.Get() {
			log.Errorf("serve hls live error: %v", FormatNotSupportFileTypeError(fileExt))
			ctx.AbortWithStatusJSON(
				http.StatusNotFound,
				model.NewAPIErrorResp(FormatNotSupportFileTypeError(fileExt)),
			)
			return
		}
		b, err := channel.GetTsFile(strings.TrimSuffix(dataID, fileExt))
		if err != nil {
			log.Errorf("serve hls live error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
			return
		}
		ctx.Header("Cache-Control", "public, max-age=90")
		ctx.Data(http.StatusOK, hls.TSContentType, b)
	case ".png":
		if !settings.TSDisguisedAsPng.Get() {
			log.Errorf("serve hls live error: %v", FormatNotSupportFileTypeError(fileExt))
			ctx.AbortWithStatusJSON(
				http.StatusNotFound,
				model.NewAPIErrorResp(FormatNotSupportFileTypeError(fileExt)),
			)
			return
		}
		b, err := channel.GetTsFile(strings.TrimSuffix(dataID, fileExt))
		if err != nil {
			log.Errorf("serve hls live error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewAPIErrorResp(err))
			return
		}
		ctx.Header("Cache-Control", "public, max-age=90")
		img := image.NewGray(image.Rect(0, 0, 1, 1))
		img.Set(1, 1, color.Gray{uint8(rand.IntN(255))})
		cache := bytes.NewBuffer(make([]byte, 0, 71))
		err = png.Encode(cache, img)
		if err != nil {
			log.Errorf("serve hls live error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewAPIErrorResp(err))
			return
		}
		ctx.Data(http.StatusOK, "image/png", append(cache.Bytes(), b...))
	default:
		ctx.Header("Cache-Control", "no-store")
		log.Errorf("serve hls live error: %v", FormatNotSupportFileTypeError(fileExt))
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorResp(FormatNotSupportFileTypeError(fileExt)),
		)
	}
}
