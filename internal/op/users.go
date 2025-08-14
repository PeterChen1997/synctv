package op

import (
	"errors"
	"hash/crc32"
	"time"

	"github.com/PeterChen1997/synctv/internal/db"
	"github.com/PeterChen1997/synctv/internal/model"
	"github.com/PeterChen1997/synctv/internal/provider"
	"github.com/zijiren233/gencontainer/synccache"
)

var userCache *synccache.SyncCache[string, *User]

type UserEntry = synccache.Entry[*User]

var (
	ErrUserBanned  = errors.New("user account has been banned")
	ErrUserPending = errors.New(
		"user account is pending approval, please wait for administrator review",
	)
)

func LoadOrInitUser(u *model.User) (*UserEntry, error) {
	i, _ := userCache.LoadOrStore(u.ID, &User{
		User:    *u,
		version: crc32.ChecksumIEEE(u.HashedPassword),
	}, time.Hour)
	return i, nil
}

func LoadOrInitUserByID(id string) (*UserEntry, error) {
	u, ok := userCache.Load(id)
	if ok {
		u.SetExpiration(time.Now().Add(time.Hour))
		return u, nil
	}

	user, err := db.GetUserByID(id)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(user)
}

func LoadOrInitUserByEmail(email string) (*UserEntry, error) {
	u, err := db.GetUserByEmail(email)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(u)
}

func LoadOrInitUserByUsername(username string) (*UserEntry, error) {
	u, err := db.GetUserByUsername(username)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(u)
}

func CreateUser(username, password string, conf ...db.CreateUserConfig) (*UserEntry, error) {
	if username == "" {
		return nil, errors.New("username cannot be empty")
	}
	u, err := db.CreateUser(username, password, conf...)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(u)
}

func CreateOrLoadUserWithProvider(
	username, password string,
	p provider.OAuth2Provider,
	pid string,
	conf ...db.CreateUserConfig,
) (*UserEntry, error) {
	u, err := db.CreateOrLoadUserWithProvider(username, password, p, pid, conf...)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(u)
}

func CreateUserWithEmail(
	username, password, email string,
	conf ...db.CreateUserConfig,
) (*UserEntry, error) {
	u, err := db.CreateUserWithEmail(username, password, email, conf...)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(u)
}

func GetUserByProvider(p provider.OAuth2Provider, pid string) (*UserEntry, error) {
	u, err := db.GetUserByProvider(p, pid)
	if err != nil {
		return nil, err
	}

	return LoadOrInitUser(u)
}

func CompareAndDeleteUser(user *UserEntry) error {
	id := user.Value().ID
	if id == db.GuestUserID {
		return errors.New("cannot delete guest user")
	}
	err := db.DeleteUserByID(id)
	if err != nil {
		return err
	}
	return CompareAndCloseUser(user)
}

func DeleteUserByID(id string) error {
	if id == db.GuestUserID {
		return errors.New("cannot delete guest user")
	}
	err := db.DeleteUserByID(id)
	if err != nil {
		return err
	}
	return CloseUserByID(id)
}

func CloseUserByID(id string) error {
	userCache.Delete(id)
	roomCache.Range(func(_ string, value *synccache.Entry[*Room]) bool {
		if value.Value().CreatorID == id {
			CompareAndCloseRoom(value)
		}
		return true
	})
	return nil
}

func CompareAndCloseUser(user *UserEntry) error {
	if !userCache.CompareAndDelete(user.Value().ID, user) {
		return nil
	}
	roomCache.Range(func(_ string, value *synccache.Entry[*Room]) bool {
		if value.Value().CreatorID == user.Value().ID {
			CompareAndCloseRoom(value)
		}
		return true
	})
	return nil
}

func GetUserName(userID string) string {
	u, err := LoadOrInitUserByID(userID)
	if err != nil {
		return ""
	}
	return u.Value().Username
}

func LoadOrInitGuestUser() (*UserEntry, error) {
	return LoadOrInitUserByID(db.GuestUserID)
}
