package vendoremby

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/PeterChen1997/synctv/internal/db"
	dbModel "github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/op"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/server/handlers/proxy"
	"github.com/PeterChen1997/synctv/server/middlewares"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/PeterChen1997/vendors/api/emby"
)

type EmbyVendorService struct {
	room  *op.Room
	movie *op.Movie
}

func NewEmbyVendorService(room *op.Room, movie *op.Movie) (*EmbyVendorService, error) {
	if movie.VendorInfo.Vendor != dbModel.VendorEmby {
		return nil, fmt.Errorf("emby vendor not support vendor %s", movie.VendorInfo.Vendor)
	}
	return &EmbyVendorService{
		room:  room,
		movie: movie,
	}, nil
}

func (s *EmbyVendorService) Client() emby.EmbyHTTPServer {
	return vendor.LoadEmbyClient(s.movie.VendorInfo.Backend)
}

//nolint:gosec
func (s *EmbyVendorService) ListDynamicMovie(
	ctx context.Context,
	reqUser *op.User,
	subPath, keyword string,
	page, _max int,
) (*model.MovieList, error) {
	if reqUser.ID != s.movie.CreatorID {
		return nil, fmt.Errorf("list vendor dynamic folder error: %w", dbModel.ErrNoPermission)
	}
	user := reqUser

	resp := &model.MovieList{
		Paths: []*model.MoviePath{},
	}

	serverID, truePath, err := s.movie.VendorInfo.Emby.ServerIDAndFilePath()
	if err != nil {
		return nil, fmt.Errorf("load emby server id error: %w", err)
	}
	if subPath != "" {
		truePath = subPath
	}
	aucd, err := user.EmbyCache().LoadOrStore(ctx, serverID)
	if err != nil {
		if errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			return nil, errors.New("emby server not found")
		}
		return nil, err
	}
	data, err := s.Client().FsList(ctx, &emby.FsListReq{
		Host:       aucd.Host,
		Path:       truePath,
		Token:      aucd.APIKey,
		UserId:     aucd.UserID,
		Limit:      uint64(_max),
		StartIndex: uint64((page - 1) * _max),
		SearchTerm: keyword,
	})
	if err != nil {
		return nil, fmt.Errorf("emby fs list error: %w", err)
	}
	resp.Total = int64(data.GetTotal())
	resp.Movies = make([]*model.Movie, len(data.GetItems()))
	for i, flr := range data.GetItems() {
		resp.Movies[i] = &model.Movie{
			ID:        s.movie.ID,
			CreatedAt: s.movie.CreatedAt.UnixMilli(),
			Creator:   op.GetUserName(s.movie.CreatorID),
			CreatorID: s.movie.CreatorID,
			SubPath:   flr.GetId(),
			Base: dbModel.MovieBase{
				Name:     flr.GetName(),
				IsFolder: flr.GetIsFolder(),
				ParentID: dbModel.EmptyNullString(s.movie.ID),
				VendorInfo: dbModel.VendorInfo{
					Vendor:  dbModel.VendorEmby,
					Backend: s.movie.VendorInfo.Backend,
					Emby: &dbModel.EmbyStreamingInfo{
						Path: dbModel.FormatEmbyPath(serverID, flr.GetId()),
					},
				},
			},
		}
	}
	return resp, nil
}

func (s *EmbyVendorService) handleProxyMovie(ctx *gin.Context) {
	log := middlewares.GetLogger(ctx)

	if !s.movie.Proxy {
		log.Errorf("proxy vendor movie error: %v", "proxy is not enabled")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("proxy is not enabled"),
		)
		return
	}

	u, err := op.LoadOrInitUserByID(s.movie.CreatorID)
	if err != nil {
		log.Errorf("proxy vendor movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp(err.Error()))
		return
	}

	embyC, err := s.movie.EmbyCache().Get(ctx, u.Value().EmbyCache())
	if err != nil {
		log.Errorf("proxy vendor movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp(err.Error()))
		return
	}

	if len(embyC.Sources) == 0 {
		log.Errorf("proxy vendor movie error: %v", "no source")
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp("no source"))
		return
	}

	source, err := strconv.Atoi(ctx.Query("source"))
	if err != nil {
		log.Errorf("proxy vendor movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp(err.Error()))
		return
	}

	if source >= len(embyC.Sources) {
		log.Errorf("proxy vendor movie error: %v", "source out of range")
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("source out of range"),
		)
		return
	}

	if embyC.Sources[source].IsTranscode {
		ctx.Redirect(http.StatusFound, embyC.Sources[source].URL)
		return
	}

	// ignore DeviceId, PlaySessionId as cache key
	sourceCacheKey, err := url.Parse(embyC.Sources[source].URL)
	if err != nil {
		log.Errorf("proxy vendor movie error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewAPIErrorStringResp(err.Error()))
		return
	}

	query := sourceCacheKey.Query()
	query.Del("DeviceId")
	query.Del("PlaySessionId")
	sourceCacheKey.RawQuery = query.Encode()

	err = proxy.AutoProxyURL(ctx,
		embyC.Sources[source].URL,
		"",
		nil,
		ctx.GetString("token"),
		s.movie.RoomID,
		s.movie.ID,
		proxy.WithProxyURLCache(true),
		proxy.WithProxyURLCacheKey(sourceCacheKey.String()),
	)
	if err != nil {
		log.Errorf("proxy vendor movie error: %v", err)
	}
}

func (s *EmbyVendorService) handleSubtitle(ctx *gin.Context) error {
	u, err := op.LoadOrInitUserByID(s.movie.CreatorID)
	if err != nil {
		return err
	}

	embyC, err := s.movie.EmbyCache().Get(ctx, u.Value().EmbyCache())
	if err != nil {
		return err
	}

	source, err := strconv.Atoi(ctx.Query("source"))
	if err != nil {
		return err
	}

	if source >= len(embyC.Sources) {
		return errors.New("source out of range")
	}

	id, err := strconv.Atoi(ctx.Query("id"))
	if err != nil {
		return err
	}

	if id >= len(embyC.Sources[source].Subtitles) {
		return errors.New("id out of range")
	}

	data, err := embyC.Sources[source].Subtitles[id].Cache.Get(ctx)
	if err != nil {
		return err
	}

	http.ServeContent(
		ctx.Writer,
		ctx.Request,
		embyC.Sources[source].Subtitles[id].Name,
		time.Now(),
		bytes.NewReader(data),
	)
	return nil
}

func (s *EmbyVendorService) ProxyMovie(ctx *gin.Context) {
	switch t := ctx.Query("t"); t {
	case "":
		s.handleProxyMovie(ctx)
	case "subtitle":
		_ = s.handleSubtitle(ctx)
	default:
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			model.NewAPIErrorStringResp("unknown proxy type: "+t),
		)
	}
}

func (s *EmbyVendorService) GenMovieInfo(
	ctx context.Context,
	user *op.User,
	userAgent, userToken string,
) (*dbModel.Movie, error) {
	if s.movie.Proxy {
		return s.GenProxyMovieInfo(ctx, user, userAgent, userToken)
	}

	movie := s.movie.Clone()
	var err error

	u, err := op.LoadOrInitUserByID(movie.CreatorID)
	if err != nil {
		return nil, err
	}
	data, err := s.movie.EmbyCache().Get(ctx, u.Value().EmbyCache())
	if err != nil {
		return nil, err
	}

	if len(data.Sources) == 0 {
		return nil, errors.New("no source")
	}
	movie.URL = data.Sources[0].URL
	for _, s := range data.Sources[0].Subtitles {
		if movie.Subtitles == nil {
			movie.Subtitles = make(map[string]*dbModel.Subtitle, len(data.Sources[0].Subtitles))
		}
		movie.Subtitles[s.Name] = &dbModel.Subtitle{
			URL:  s.URL,
			Type: s.Type,
		}
	}
	for _, s := range data.Sources[1:] {
		movie.MoreSources = append(movie.MoreSources,
			&dbModel.MoreSource{
				Name: s.Name,
				URL:  s.URL,
			},
		)

		for _, subt := range s.Subtitles {
			if movie.Subtitles == nil {
				movie.Subtitles = make(map[string]*dbModel.Subtitle, len(s.Subtitles))
			}
			movie.Subtitles[subt.Name] = &dbModel.Subtitle{
				URL:  subt.URL,
				Type: subt.Type,
			}
		}
	}

	return movie, nil
}

func (s *EmbyVendorService) GenProxyMovieInfo(
	ctx context.Context,
	_ *op.User,
	_, userToken string,
) (*dbModel.Movie, error) {
	movie := s.movie.Clone()
	var err error

	u, err := op.LoadOrInitUserByID(movie.CreatorID)
	if err != nil {
		return nil, err
	}
	data, err := s.movie.EmbyCache().Get(ctx, u.Value().EmbyCache())
	if err != nil {
		return nil, err
	}

	for si, es := range data.Sources {
		if len(es.URL) == 0 {
			if si != len(data.Sources)-1 {
				continue
			}
			if movie.URL == "" {
				return nil, errors.New("no source")
			}
		}

		rawPath, err := url.JoinPath("/api/room/movie/proxy", movie.ID)
		if err != nil {
			return nil, err
		}
		rawQuery := url.Values{}
		rawQuery.Set("source", strconv.Itoa(si))
		rawQuery.Set("token", userToken)
		rawQuery.Set("roomId", movie.RoomID)
		u := url.URL{
			Path:     rawPath,
			RawQuery: rawQuery.Encode(),
		}

		if si == 0 {
			movie.URL = u.String()
			movie.Type = utils.GetURLExtension(es.URL)
		} else {
			movie.MoreSources = append(movie.MoreSources,
				&dbModel.MoreSource{
					Name: es.Name,
					URL:  u.String(),
					Type: utils.GetURLExtension(es.URL),
				},
			)
		}

		if len(es.Subtitles) == 0 {
			continue
		}
		for sbi, s := range es.Subtitles {
			if movie.Subtitles == nil {
				movie.Subtitles = make(map[string]*dbModel.Subtitle, len(es.Subtitles))
			}
			rawQuery := url.Values{}
			rawQuery.Set("t", "subtitle")
			rawQuery.Set("source", strconv.Itoa(si))
			rawQuery.Set("id", strconv.Itoa(sbi))
			rawQuery.Set("token", userToken)
			rawQuery.Set("roomId", movie.RoomID)
			u := url.URL{
				Path:     rawPath,
				RawQuery: rawQuery.Encode(),
			}
			movie.Subtitles[s.Name] = &dbModel.Subtitle{
				URL:  u.String(),
				Type: s.Type,
			}
		}
	}

	return movie, nil
}
