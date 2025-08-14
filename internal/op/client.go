package op

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/PeterChen1997/synctv/internal/model"
	pb "github.com/PeterChen1997/synctv/proto/message"
)

type Client struct {
	u         *User
	r         *Room
	h         *Hub
	c         chan Message
	conn      *websocket.Conn
	connID    string
	wg        sync.WaitGroup
	timeOut   time.Duration
	closed    uint32
	rtcJoined atomic.Bool
}

func newClient(user *User, room *Room, h *Hub, conn *websocket.Conn) *Client {
	return &Client{
		connID:  uuid.New().String(),
		r:       room,
		u:       user,
		h:       h,
		c:       make(chan Message, 128),
		conn:    conn,
		timeOut: 10 * time.Second,
	}
}

func (c *Client) ConnID() string {
	return c.connID
}

func (c *Client) RTCJoined() bool {
	return c.rtcJoined.Load()
}

func (c *Client) SetRTCJoined(joined bool) {
	c.rtcJoined.Store(joined)
}

func (c *Client) User() *User {
	return c.u
}

func (c *Client) Room() *Room {
	return c.r
}

func (c *Client) Broadcast(msg Message, conf ...BroadcastConf) error {
	return c.h.Broadcast(msg, conf...)
}

func (c *Client) SendChatMessage(message string) error {
	if !c.u.HasRoomPermission(c.r, model.PermissionSendChatMessage) {
		return model.ErrNoPermission
	}
	return c.Broadcast(&pb.Message{
		Type:      pb.MessageType_CHAT,
		Timestamp: time.Now().UnixMilli(),
		Sender: &pb.Sender{
			UserId:   c.u.ID,
			Username: c.u.Username,
		},
		Payload: &pb.Message_ChatContent{
			ChatContent: message,
		},
	})
}

func (c *Client) Send(msg Message) error {
	c.wg.Add(1)
	defer c.wg.Done()
	if c.Closed() {
		return ErrAlreadyClosed
	}
	c.c <- msg
	return nil
}

func (c *Client) Close() error {
	if !atomic.CompareAndSwapUint32(&c.closed, 0, 1) {
		return ErrAlreadyClosed
	}
	c.wg.Wait()
	close(c.c)
	return nil
}

func (c *Client) Closed() bool {
	return atomic.LoadUint32(&c.closed) == 1
}

func (c *Client) GetReadChan() <-chan Message {
	return c.c
}

func (c *Client) NextWriter(messageType int) (io.WriteCloser, error) {
	return c.conn.NextWriter(messageType)
}

func (c *Client) NextReader() (int, io.Reader, error) {
	return c.conn.NextReader()
}

func (c *Client) SetStatus(playing bool, seek, rate, timeDiff float64) error {
	status, err := c.u.SetRoomCurrentStatus(c.r, playing, seek, rate, timeDiff)
	if err != nil {
		return err
	}
	return c.Broadcast(&pb.Message{
		Type: pb.MessageType_STATUS,
		Sender: &pb.Sender{
			Username: c.User().Username,
			UserId:   c.User().ID,
		},
		Payload: &pb.Message_PlaybackStatus{
			PlaybackStatus: &pb.Status{
				IsPlaying:    status.IsPlaying,
				CurrentTime:  status.CurrentTime,
				PlaybackRate: status.PlaybackRate,
			},
		},
	}, WithIgnoreConnID(c.ConnID()))
}
