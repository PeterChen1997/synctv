package middlewares

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/PeterChen1997/synctv/internal/conf"
	dbModel "github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/op"
	"github.com/PeterChen1997/synctv/internal/settings"
	"github.com/PeterChen1997/synctv/server/model"
	"github.com/zijiren233/gencontainer/synccache"
	"github.com/zijiren233/stream"
)

var (
	ErrAuthFailed         = errors.New("authentication failed")
	ErrAuthExpired        = errors.New("authentication token expired")
	ErrUserBanned         = errors.New("user account has been banned")
	ErrUserPending        = errors.New("user account is pending approval")
	ErrUserGuest          = errors.New("guests are not allowed to perform this action")
	ErrRoomBanned         = errors.New("room has been banned")
	ErrRoomPending        = errors.New("room is pending approval")
	ErrUserBannedFromRoom = errors.New("user has been banned from this room")
	ErrInvalidRoomID      = errors.New("invalid room ID")
	ErrEmptyToken         = errors.New("authentication token is empty")
	ErrNotRoomAdmin       = errors.New("user is not a room administrator")
	ErrNotRoomCreator     = errors.New("user is not the room creator")
	ErrNotAdmin           = errors.New("user is not an administrator")
	ErrNotRoot            = errors.New("user is not a root user")
)

type AuthClaims struct {
	jwt.RegisteredClaims
	UserID      string `json:"u"`
	UserVersion uint32 `json:"uv"`
}

func authUser(authorization string) (*AuthClaims, error) {
	t, err := jwt.ParseWithClaims(
		strings.TrimPrefix(authorization, `Bearer `),
		&AuthClaims{},
		func(_ *jwt.Token) (any, error) {
			return stream.StringToBytes(conf.Conf.Jwt.Secret), nil
		},
	)
	if err != nil || !t.Valid {
		return nil, ErrAuthFailed
	}
	claims, ok := t.Claims.(*AuthClaims)
	if !ok {
		return nil, ErrAuthFailed
	}
	return claims, nil
}

func AuthRoom(authorization, roomID string) (*op.UserEntry, *op.RoomEntry, error) {
	if len(roomID) != 32 {
		return nil, nil, ErrInvalidRoomID
	}

	userE, err := authenticateUserOrGuest(authorization)
	if err != nil {
		return nil, nil, err
	}

	user := userE.Value()
	roomE, err := authenticateRoomAccess(roomID, user)
	if err != nil {
		return nil, nil, err
	}

	return userE, roomE, nil
}

func authenticateUserOrGuest(authorization string) (*op.UserEntry, error) {
	if authorization != "" {
		return authenticateUser(authorization)
	}
	return authenticateGuest()
}

func authenticateUser(authorization string) (*op.UserEntry, error) {
	claims, err := authUser(authorization)
	if err != nil {
		return nil, err
	}

	if len(claims.UserID) != 32 {
		return nil, ErrAuthFailed
	}

	userE, err := op.LoadOrInitUserByID(claims.UserID)
	if err != nil {
		return nil, err
	}
	user := userE.Value()

	if err := validateUser(user, claims.UserVersion); err != nil {
		return nil, err
	}

	return userE, nil
}

func authenticateGuest() (*op.UserEntry, error) {
	if !settings.EnableGuest.Get() {
		return nil, ErrUserGuest
	}
	return op.LoadOrInitGuestUser()
}

func validateUser(user *op.User, userVersion uint32) error {
	if user.IsGuest() {
		return ErrUserGuest
	}
	if !user.CheckVersion(userVersion) {
		return ErrAuthExpired
	}
	if user.IsBanned() {
		return ErrUserBanned
	}
	if user.IsPending() {
		return ErrUserPending
	}
	return nil
}

func authenticateRoomAccess(roomID string, user *op.User) (*op.RoomEntry, error) {
	roomE, err := op.LoadOrInitRoomByID(roomID)
	if err != nil {
		return nil, err
	}
	room := roomE.Value()

	if err := validateRoomAccess(room, user); err != nil {
		return nil, err
	}

	return roomE, nil
}

func validateRoomAccess(room *op.Room, user *op.User) error {
	if room.IsGuest(user.ID) {
		if room.Settings.DisableGuest {
			return ErrUserGuest
		}
		if room.NeedPassword() {
			return ErrUserGuest
		}
	}

	if room.IsBanned() {
		return ErrRoomBanned
	}
	if room.IsPending() {
		return ErrRoomPending
	}

	var status dbModel.RoomMemberStatus
	var err error
	if room.NeedPassword() {
		status, err = room.LoadMemberStatus(user.ID)
	} else {
		status, err = room.LoadOrCreateMemberStatus(user.ID)
	}
	if err != nil {
		return err
	}

	if status.IsBanned() {
		return ErrUserBannedFromRoom
	}
	if status.IsPending() {
		return ErrUserPending
	}

	return nil
}

func AuthUser(authorization string) (*op.UserEntry, error) {
	claims, err := authUser(authorization)
	if err != nil {
		return nil, err
	}

	if len(claims.UserID) != 32 {
		return nil, ErrAuthFailed
	}

	userE, err := op.LoadOrInitUserByID(claims.UserID)
	if err != nil {
		return nil, err
	}
	user := userE.Value()

	if err := validateAuthUser(user, claims.UserVersion); err != nil {
		return nil, err
	}

	return userE, nil
}

func validateAuthUser(user *op.User, userVersion uint32) error {
	if user.IsGuest() {
		return ErrUserGuest
	}
	if !user.CheckVersion(userVersion) {
		return ErrAuthExpired
	}
	if user.IsBanned() {
		return ErrUserBanned
	}
	if user.IsPending() {
		return ErrUserPending
	}
	return nil
}

func NewAuthUserToken(user *op.User) (string, error) {
	if err := validateNewAuthUserToken(user); err != nil {
		return "", err
	}

	t, err := time.ParseDuration(conf.Conf.Jwt.Expire)
	if err != nil {
		return "", err
	}

	claims := &AuthClaims{
		UserID:      user.ID,
		UserVersion: user.Version(),
		RegisteredClaims: jwt.RegisteredClaims{
			NotBefore: jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(t)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString(stream.StringToBytes(conf.Conf.Jwt.Secret))
}

func validateNewAuthUserToken(user *op.User) error {
	if user.IsBanned() {
		return ErrUserBanned
	}
	if user.IsPending() {
		return ErrUserPending
	}
	if user.IsGuest() {
		return ErrUserGuest
	}
	return nil
}

func AuthUserMiddleware(ctx *gin.Context) {
	token := GetAuthorizationTokenFromContext(ctx)
	if token == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, model.NewAPIErrorResp(ErrEmptyToken))
		return
	}
	userE, err := AuthUser(token)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, model.NewAPIErrorResp(err))
		return
	}
	user := userE.Value()

	ctx.Set("user", userE)
	setLogFields(ctx, user, nil)
}

func AuthRoomMiddleware(ctx *gin.Context) {
	roomID, err := GetRoomIDFromContext(ctx)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, model.NewAPIErrorResp(err))
		return
	}
	userE, roomE, err := AuthRoom(GetAuthorizationTokenFromContext(ctx), roomID)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, model.NewAPIErrorResp(err))
		return
	}
	user := userE.Value()
	room := roomE.Value()

	ctx.Set("user", userE)
	ctx.Set("room", roomE)
	setLogFields(ctx, user, room)
}

func AuthRoomWithoutGuestMiddleware(ctx *gin.Context) {
	AuthRoomMiddleware(ctx)
	if ctx.IsAborted() {
		return
	}

	userEntry, ok := ctx.MustGet("user").(*synccache.Entry[*op.User])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid user type")),
		)
		return
	}
	user := userEntry.Value()
	if user.IsGuest() {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewAPIErrorResp(ErrUserGuest))
		return
	}
}

func AuthRoomAdminMiddleware(ctx *gin.Context) {
	AuthRoomMiddleware(ctx)
	if ctx.IsAborted() {
		return
	}

	roomEntry, ok := ctx.MustGet("room").(*synccache.Entry[*op.Room])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid room type")),
		)
		return
	}
	room := roomEntry.Value()
	userEntry, ok := ctx.MustGet("user").(*synccache.Entry[*op.User])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid user type")),
		)
		return
	}
	user := userEntry.Value()

	if !user.IsRoomAdmin(room) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewAPIErrorResp(ErrNotRoomAdmin))
		return
	}
}

func AuthRoomCreatorMiddleware(ctx *gin.Context) {
	AuthRoomMiddleware(ctx)
	if ctx.IsAborted() {
		return
	}

	roomEntry, ok := ctx.MustGet("room").(*synccache.Entry[*op.Room])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid room type")),
		)
		return
	}
	room := roomEntry.Value()
	userEntry, ok := ctx.MustGet("user").(*synccache.Entry[*op.User])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid user type")),
		)
		return
	}
	user := userEntry.Value()

	if room.CreatorID != user.ID {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewAPIErrorResp(ErrNotRoomCreator))
		return
	}
}

func AuthAdminMiddleware(ctx *gin.Context) {
	AuthUserMiddleware(ctx)
	if ctx.IsAborted() {
		return
	}

	userEntry, ok := ctx.MustGet("user").(*synccache.Entry[*op.User])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid user type")),
		)
		return
	}
	user := userEntry.Value()
	if !user.IsAdmin() {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewAPIErrorResp(ErrNotAdmin))
		return
	}
}

func AuthRootMiddleware(ctx *gin.Context) {
	AuthUserMiddleware(ctx)
	if ctx.IsAborted() {
		return
	}

	userEntry, ok := ctx.MustGet("user").(*synccache.Entry[*op.User])
	if !ok {
		ctx.JSON(
			http.StatusInternalServerError,
			model.NewAPIErrorResp(errors.New("invalid user type")),
		)
		return
	}
	user := userEntry.Value()
	if !user.IsRoot() {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewAPIErrorResp(ErrNotRoot))
		return
	}
}

func GetAuthorizationTokenFromContext(ctx *gin.Context) string {
	sources := []func() string{
		func() string { return ctx.GetHeader("Authorization") },
		func() string {
			if ctx.IsWebsocket() {
				return ctx.GetHeader("Sec-WebSocket-Protocol")
			}
			return ""
		},
		func() string { return ctx.Query("token") },
	}

	for _, source := range sources {
		if token := source(); token != "" {
			ctx.Set("token", token)
			return token
		}
	}

	ctx.Set("token", "")
	return ""
}

func GetRoomIDFromContext(ctx *gin.Context) (string, error) {
	sources := []func() string{
		func() string { return ctx.GetHeader("X-Room-Id") },
		func() string { return ctx.Query("roomId") },
		func() string { return ctx.Param("roomId") },
	}

	for _, source := range sources {
		roomID := source()
		if roomID == "" {
			continue
		}
		if len(roomID) == 32 {
			ctx.Set("roomId", roomID)
			return roomID, nil
		}
		ctx.Set("roomId", "")
		return "", ErrInvalidRoomID
	}

	ctx.Set("roomId", "")
	return "", ErrInvalidRoomID
}

func setLogFields(ctx *gin.Context, user *op.User, room *op.Room) {
	log := GetLogger(ctx)
	if user != nil {
		log.Data["uid"] = user.ID
		log.Data["unm"] = user.Username
		log.Data["uro"] = user.Role.String()
	}
	if room != nil {
		log.Data["rid"] = room.ID
		log.Data["rnm"] = room.Name
	}
}

func GetUserEntry(ctx *gin.Context) *op.UserEntry {
	userEntry, ok := ctx.MustGet("user").(*synccache.Entry[*op.User])
	if !ok {
		panic("invalid user type")
	}
	return userEntry
}

func GetRoomEntry(ctx *gin.Context) *op.RoomEntry {
	roomEntry, ok := ctx.MustGet("room").(*synccache.Entry[*op.Room])
	if !ok {
		panic("invalid room type")
	}
	return roomEntry
}

func GetToken(ctx *gin.Context) string {
	token, ok := ctx.Get("token")
	if !ok {
		return ""
	}
	t, ok := token.(string)
	if !ok {
		panic("invalid token type")
	}
	return t
}
