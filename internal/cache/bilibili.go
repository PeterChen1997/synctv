package cache

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/PeterChen1997/synctv/internal/db"
	"github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/vendor"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/PeterChen1997/vendors/api/bilibili"
	"github.com/zencoder/go-dash/v3/mpd"
	"github.com/zijiren233/gencontainer/refreshcache"
	"github.com/zijiren233/gencontainer/refreshcache0"
	"github.com/zijiren233/gencontainer/refreshcache1"
	"github.com/zijiren233/go-uhc"
)

type BilibiliMpdCache struct {
	Mpd     *mpd.MPD
	HevcMpd *mpd.MPD
	URLs    []string
}

type BilibiliSubtitleCache map[string]*BilibiliSubtitleCacheItem

type BilibiliSubtitleCacheItem struct {
	Srt *refreshcache0.RefreshCache[[]byte]
	URL string
}

func NewBilibiliSharedMpdCacheInitFunc(
	movie *model.Movie,
) func(ctx context.Context, args *BilibiliUserCache) (*BilibiliMpdCache, error) {
	return func(ctx context.Context, args *BilibiliUserCache) (*BilibiliMpdCache, error) {
		return BilibiliSharedMpdCacheInitFunc(ctx, movie, args)
	}
}

func BilibiliSharedMpdCacheInitFunc(
	ctx context.Context,
	movie *model.Movie,
	args *BilibiliUserCache,
) (*BilibiliMpdCache, error) {
	if args == nil {
		return nil, errors.New("no bilibili user cache data")
	}

	cookies, err := getBilibiliCookies(ctx, args)
	if err != nil {
		return nil, err
	}

	cli := vendor.LoadBilibiliClient(movie.VendorInfo.Backend)
	m, hevcM, err := getBilibiliMpd(ctx, cli, movie.VendorInfo.Bilibili, cookies)
	if err != nil {
		return nil, err
	}

	m.BaseURL = append(m.BaseURL, "/api/room/movie/proxy/")
	movies := processMpdUrls(m, hevcM, movie.ID, movie.RoomID)

	return &BilibiliMpdCache{
		URLs:    movies,
		Mpd:     m,
		HevcMpd: hevcM,
	}, nil
}

func getBilibiliCookies(ctx context.Context, args *BilibiliUserCache) ([]*http.Cookie, error) {
	vendorInfo, err := args.Get(ctx)
	if err != nil {
		if !errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			return nil, err
		}
		return nil, nil
	}
	return vendorInfo.Cookies, nil
}

func getBilibiliMpd(
	ctx context.Context,
	cli bilibili.BilibiliHTTPServer,
	biliInfo *model.BilibiliStreamingInfo,
	cookies []*http.Cookie,
) (*mpd.MPD, *mpd.MPD, error) {
	cookiesMap := utils.HTTPCookieToMap(cookies)

	switch {
	case biliInfo.Epid != 0:
		resp, err := cli.GetDashPGCURL(ctx, &bilibili.GetDashPGCURLReq{
			Cookies: cookiesMap,
			Epid:    biliInfo.Epid,
		})
		if err != nil {
			return nil, nil, err
		}
		return parseMpdResponse(resp.GetMpd(), resp.GetHevcMpd())

	case biliInfo.Bvid != "":
		resp, err := cli.GetDashVideoURL(ctx, &bilibili.GetDashVideoURLReq{
			Cookies: cookiesMap,
			Bvid:    biliInfo.Bvid,
			Cid:     biliInfo.Cid,
		})
		if err != nil {
			return nil, nil, err
		}
		return parseMpdResponse(resp.GetMpd(), resp.GetHevcMpd())

	default:
		return nil, nil, errors.New("bvid and epid are empty")
	}
}

func parseMpdResponse(mpdStr, hevcMpdStr string) (*mpd.MPD, *mpd.MPD, error) {
	m, err := mpd.ReadFromString(mpdStr)
	if err != nil {
		return nil, nil, err
	}

	hevcM, err := mpd.ReadFromString(hevcMpdStr)
	if err != nil {
		return nil, nil, err
	}

	return m, hevcM, nil
}

func processMpdUrls(m, hevcM *mpd.MPD, movieID, roomID string) []string {
	movies := []string{}
	id := 0

	// Process regular MPD
	for _, p := range m.Periods {
		for _, as := range p.AdaptationSets {
			for _, r := range as.Representations {
				for i := range r.BaseURL {
					movies = append(movies, r.BaseURL[i])
					r.BaseURL[i] = fmt.Sprintf("%s?id=%d&roomId=%s", movieID, id, roomID)
					id++
				}
			}
		}
	}

	// Process HEVC MPD
	for _, p := range hevcM.Periods {
		for _, as := range p.AdaptationSets {
			for _, r := range as.Representations {
				for i := range r.BaseURL {
					movies = append(movies, r.BaseURL[i])
					r.BaseURL[i] = fmt.Sprintf("%s?id=%d&roomId=%s&t=hevc", movieID, id, roomID)
					id++
				}
			}
		}
	}

	return movies
}

func BilibiliMpdToString(mpdRaw *mpd.MPD, token string) (string, error) {
	newMpdRaw := *mpdRaw
	newPeriods := make([]*mpd.Period, len(mpdRaw.Periods))
	for i, p := range mpdRaw.Periods {
		n := *p
		newPeriods[i] = &n
	}
	newMpdRaw.Periods = newPeriods
	for _, p := range newMpdRaw.Periods {
		newAdaptationSets := make([]*mpd.AdaptationSet, len(p.AdaptationSets))
		for i, as := range p.AdaptationSets {
			n := *as
			newAdaptationSets[i] = &n
		}
		p.AdaptationSets = newAdaptationSets
		for _, as := range p.AdaptationSets {
			newRepresentations := make([]*mpd.Representation, len(as.Representations))
			for i, r := range as.Representations {
				n := *r
				newRepresentations[i] = &n
			}
			as.Representations = newRepresentations
			for _, r := range as.Representations {
				newBaseURL := make([]string, len(r.BaseURL))
				copy(newBaseURL, r.BaseURL)
				r.BaseURL = newBaseURL
				for i := range r.BaseURL {
					r.BaseURL[i] = fmt.Sprintf("%s&token=%s", r.BaseURL[i], token)
				}
			}
		}
	}
	return newMpdRaw.WriteToString()
}

func NewBilibiliNoSharedMovieCacheInitFunc(
	movie *model.Movie,
) func(ctx context.Context, _ string, args ...*BilibiliUserCache) (string, error) {
	return func(ctx context.Context, _ string, args ...*BilibiliUserCache) (string, error) {
		return BilibiliNoSharedMovieCacheInitFunc(ctx, movie, args...)
	}
}

func BilibiliNoSharedMovieCacheInitFunc(
	ctx context.Context,
	movie *model.Movie,
	args ...*BilibiliUserCache,
) (string, error) {
	if len(args) == 0 {
		return "", errors.New("no bilibili user cache data")
	}
	var cookies []*http.Cookie
	vendorInfo, err := args[0].Get(ctx)
	if err != nil {
		if !errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			return "", err
		}
	} else {
		cookies = vendorInfo.Cookies
	}
	cli := vendor.LoadBilibiliClient(movie.VendorInfo.Backend)
	var u string
	biliInfo := movie.VendorInfo.Bilibili
	switch {
	case biliInfo.Epid != 0:
		resp, err := cli.GetPGCURL(ctx, &bilibili.GetPGCURLReq{
			Cookies: utils.HTTPCookieToMap(cookies),
			Epid:    biliInfo.Epid,
		})
		if err != nil {
			return "", err
		}
		u = resp.GetUrl()

	case biliInfo.Bvid != "":
		resp, err := cli.GetVideoURL(ctx, &bilibili.GetVideoURLReq{
			Cookies: utils.HTTPCookieToMap(cookies),
			Bvid:    biliInfo.Bvid,
			Cid:     biliInfo.Cid,
		})
		if err != nil {
			return "", err
		}
		u = resp.GetUrl()

	default:
		return "", errors.New("bvid and epid are empty")
	}

	return u, nil
}

//nolint:tagliatelle
type bilibiliSubtitleResp struct {
	FontColor       string `json:"font_color"`
	BackgroundColor string `json:"background_color"`
	Stroke          string `json:"Stroke"`
	Type            string `json:"type"`
	Lang            string `json:"lang"`
	Version         string `json:"version"`
	Body            []struct {
		Content  string  `json:"content"`
		From     float64 `json:"from"`
		To       float64 `json:"to"`
		Sid      int     `json:"sid"`
		Location int     `json:"location"`
	} `json:"body"`
	FontSize        float64 `json:"font_size"`
	BackgroundAlpha float64 `json:"background_alpha"`
}

func NewBilibiliSubtitleCacheInitFunc(
	movie *model.Movie,
) func(ctx context.Context, args *BilibiliUserCache) (BilibiliSubtitleCache, error) {
	return func(ctx context.Context, args *BilibiliUserCache) (BilibiliSubtitleCache, error) {
		return BilibiliSubtitleCacheInitFunc(ctx, movie, args)
	}
}

func BilibiliSubtitleCacheInitFunc(
	ctx context.Context,
	movie *model.Movie,
	args *BilibiliUserCache,
) (BilibiliSubtitleCache, error) {
	if args == nil {
		return nil, errors.New("no bilibili user cache data")
	}

	biliInfo := movie.VendorInfo.Bilibili
	if biliInfo.Bvid == "" || biliInfo.Cid == 0 {
		return nil, errors.New("bvid or cid is empty")
	}

	// must login
	var cookies []*http.Cookie
	vendorInfo, err := args.Get(ctx)
	if err != nil {
		if errors.Is(err, db.NotFoundError(db.ErrVendorNotFound)) {
			return make(BilibiliSubtitleCache), nil
		}
		return nil, err
	}
	cookies = vendorInfo.Cookies

	cli := vendor.LoadBilibiliClient(movie.VendorInfo.Backend)
	resp, err := cli.GetSubtitles(ctx, &bilibili.GetSubtitlesReq{
		Cookies: utils.HTTPCookieToMap(cookies),
		Bvid:    biliInfo.Bvid,
		Cid:     biliInfo.Cid,
	})
	if err != nil {
		return nil, err
	}
	subtitleCache := make(BilibiliSubtitleCache, len(resp.GetSubtitles()))
	for k, v := range resp.GetSubtitles() {
		subtitleCache[k] = &BilibiliSubtitleCacheItem{
			URL: v,
			Srt: refreshcache0.NewRefreshCache(func(ctx context.Context) ([]byte, error) {
				return translateBilibiliSubtitleToSrt(ctx, v)
			}, 0),
		}
	}

	return subtitleCache, nil
}

func convertToSRT(subtitles *bilibiliSubtitleResp) []byte {
	srt := bytes.NewBuffer(nil)
	counter := 0
	for _, subtitle := range subtitles.Body {
		fmt.Fprintf(srt,
			"%d\n%s --> %s\n%s\n\n",
			counter,
			formatTime(subtitle.From),
			formatTime(subtitle.To),
			subtitle.Content)
		counter++
	}
	return srt.Bytes()
}

func formatTime(seconds float64) string {
	hours := int(seconds) / 3600
	seconds = math.Mod(seconds, 3600)
	minutes := int(seconds) / 60
	seconds = math.Mod(seconds, 60)
	milliseconds := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, int(seconds), milliseconds)
}

func translateBilibiliSubtitleToSrt(ctx context.Context, url string) ([]byte, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "https:"+url, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", utils.UA)
	r.Header.Set("Referer", "https://www.bilibili.com")
	resp, err := uhc.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var srt bilibiliSubtitleResp
	err = json.NewDecoder(resp.Body).Decode(&srt)
	if err != nil {
		return nil, err
	}
	return convertToSRT(&srt), nil
}

func NewBilibiliLiveCacheInitFunc(movie *model.Movie) func(ctx context.Context) ([]byte, error) {
	return func(ctx context.Context) ([]byte, error) {
		return BilibiliLiveCacheInitFunc(ctx, movie)
	}
}

func genBilibiliLiveM3U8ListFile(urls []*bilibili.LiveStream) []byte {
	buf := bytes.NewBuffer(nil)
	buf.WriteString("#EXTM3U\n")
	buf.WriteString("#EXT-X-VERSION:3\n")
	for _, v := range urls {
		if len(v.GetUrls()) == 0 {
			continue
		}
		fmt.Fprintf(
			buf,
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,NAME=\"%s\"\n",
			1920*1080*v.GetQuality(),
			v.GetDesc(),
		)
		buf.WriteString(v.GetUrls()[0] + "\n")
	}
	return buf.Bytes()
}

func BilibiliLiveCacheInitFunc(ctx context.Context, movie *model.Movie) ([]byte, error) {
	cli := vendor.LoadBilibiliClient(movie.VendorInfo.Backend)
	resp, err := cli.GetLiveStreams(ctx, &bilibili.GetLiveStreamsReq{
		Cid: movie.VendorInfo.Bilibili.Cid,
		Hls: true,
	})
	if err != nil {
		return nil, err
	}
	return genBilibiliLiveM3U8ListFile(resp.GetLiveStreams()), nil
}

func NewBilibiliDanmuCacheInitFunc(movie *model.Movie) func(ctx context.Context) ([]byte, error) {
	return func(ctx context.Context) ([]byte, error) {
		return BilibiliDanmuCacheInitFunc(ctx, movie)
	}
}

func BilibiliDanmuCacheInitFunc(ctx context.Context, movie *model.Movie) ([]byte, error) {
	u := fmt.Sprintf("https://comment.bilibili.com/%d.xml", movie.VendorInfo.Bilibili.Cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	gz := flate.NewReader(resp.Body)
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	return data, nil
}

type BilibiliMovieCache struct {
	NoSharedMovie *MapCache[string, *BilibiliUserCache]
	SharedMpd     *refreshcache1.RefreshCache[*BilibiliMpdCache, *BilibiliUserCache]
	Subtitle      *refreshcache1.RefreshCache[BilibiliSubtitleCache, *BilibiliUserCache]
	Live          *refreshcache0.RefreshCache[[]byte]
	Danmu         *refreshcache0.RefreshCache[[]byte]
}

func NewBilibiliMovieCache(movie *model.Movie) *BilibiliMovieCache {
	return &BilibiliMovieCache{
		NoSharedMovie: newMapCache(NewBilibiliNoSharedMovieCacheInitFunc(movie), time.Minute*55),
		SharedMpd: refreshcache1.NewRefreshCache(
			NewBilibiliSharedMpdCacheInitFunc(movie),
			time.Minute*55,
		),
		Subtitle: refreshcache1.NewRefreshCache(NewBilibiliSubtitleCacheInitFunc(movie), -1),
		Live: refreshcache0.NewRefreshCache(
			NewBilibiliLiveCacheInitFunc(movie),
			time.Minute*55,
		),
		Danmu: refreshcache0.NewRefreshCache(NewBilibiliDanmuCacheInitFunc(movie), -1),
	}
}

type BilibiliUserCache = refreshcache.RefreshCache[*BilibiliUserCacheData, struct{}]

type BilibiliUserCacheData struct {
	Backend string
	Cookies []*http.Cookie
}

func NewBilibiliUserCache(userID string) *BilibiliUserCache {
	f := BilibiliAuthorizationCacheWithUserIDInitFunc(userID)
	return refreshcache.NewRefreshCache(
		func(ctx context.Context, _ ...struct{}) (*BilibiliUserCacheData, error) {
			return f(ctx)
		},
		-1,
	)
}

func BilibiliAuthorizationCacheWithUserIDInitFunc(
	userID string,
) func(ctx context.Context, _ ...struct{}) (*BilibiliUserCacheData, error) {
	return func(_ context.Context, _ ...struct{}) (*BilibiliUserCacheData, error) {
		v, err := db.GetBilibiliVendor(userID)
		if err != nil {
			return nil, err
		}
		return &BilibiliUserCacheData{
			Cookies: utils.MapToHTTPCookie(v.Cookies),
			Backend: v.Backend,
		}, nil
	}
}
