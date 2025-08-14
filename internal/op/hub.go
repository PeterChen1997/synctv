package op

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	pb "github.com/PeterChen1997/synctv/proto/message"
	"github.com/PeterChen1997/synctv/utils"
	"github.com/zijiren233/gencontainer/rwmap"
)

type clients struct {
	m    map[string]*Client
	lock sync.RWMutex
}

type Hub struct {
	broadcast chan *broadcastMessage
	exit      chan struct{}
	id        string
	clients   rwmap.RWMap[string, *clients]
	wg        sync.WaitGroup
	once      utils.Once
	closed    uint32
}

type broadcastMessage struct {
	data         Message
	ignoreConnID []string
	ignoreUserID []string
	rtcJoined    bool
}

type BroadcastConf func(*broadcastMessage)

func WithRTCJoined() BroadcastConf {
	return func(bm *broadcastMessage) {
		bm.rtcJoined = true
	}
}

func WithIgnoreConnID(connID ...string) BroadcastConf {
	return func(bm *broadcastMessage) {
		bm.ignoreConnID = connID
	}
}

func WithIgnoreID(id ...string) BroadcastConf {
	return func(bm *broadcastMessage) {
		bm.ignoreUserID = id
	}
}

func newHub(id string) *Hub {
	return &Hub{
		id:        id,
		broadcast: make(chan *broadcastMessage, 128),
		exit:      make(chan struct{}),
	}
}

func (h *Hub) Start() error {
	h.once.Do(func() {
		go h.serve()
		go h.ping()
	})
	return nil
}

func (h *Hub) serve() {
	for {
		select {
		case message := <-h.broadcast:
			h.devMessage(message.data)
			h.clients.Range(func(_ string, clients *clients) bool {
				clients.lock.RLock()
				defer clients.lock.RUnlock()
				for _, c := range clients.m {
					if utils.In(message.ignoreUserID, c.u.ID) ||
						utils.In(message.ignoreConnID, c.ConnID()) {
						continue
					}
					if message.rtcJoined && !c.RTCJoined() {
						continue
					}
					if err := c.Send(message.data); err != nil {
						c.Close()
					}
				}

				return true
			})
		case <-h.exit:
			log.Debugf("hub: %s, closed", h.id)
			return
		}
	}
}

func (h *Hub) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()
	var (
		pre     int64
		current int64
	)
	for {
		select {
		case <-ticker.C:
			current = h.ClientNum()
			if current != pre {
				if err := h.Broadcast(&pb.Message{
					Type: pb.MessageType_VIEWER_COUNT,
					Payload: &pb.Message_ViewerCount{
						ViewerCount: current,
					},
				}); err != nil {
					continue
				}
				pre = current
			} else {
				if err := h.Broadcast(&PingMessage{}); err != nil {
					continue
				}
			}
		case <-h.exit:
			return
		}
	}
}

func (h *Hub) devMessage(msg Message) {
	switch msg.MessageType() {
	case websocket.BinaryMessage:
		log.Debugf("hub: %s, broadcast:\nmessage: %+v", h.id, msg.String())
	default:
	}
}

func (h *Hub) Closed() bool {
	return atomic.LoadUint32(&h.closed) == 1
}

var ErrAlreadyClosed = errors.New("already closed")

func (h *Hub) Close() error {
	if !atomic.CompareAndSwapUint32(&h.closed, 0, 1) {
		return ErrAlreadyClosed
	}
	close(h.exit)
	h.clients.Range(func(id string, clients *clients) bool {
		h.clients.CompareAndDelete(id, clients)
		clients.lock.Lock()
		defer clients.lock.Unlock()
		for id, c := range clients.m {
			delete(clients.m, id)
			c.Close()
		}
		return true
	})
	h.wg.Wait()
	close(h.broadcast)
	return nil
}

func (h *Hub) Broadcast(data Message, conf ...BroadcastConf) error {
	h.wg.Add(1)
	defer h.wg.Done()
	if h.Closed() {
		return ErrAlreadyClosed
	}
	h.once.Done()
	msg := &broadcastMessage{data: data}
	for _, c := range conf {
		c(msg)
	}
	select {
	case h.broadcast <- msg:
		return nil
	case <-h.exit:
		return ErrAlreadyClosed
	}
}

func (h *Hub) RegClient(cli *Client) error {
	if h.Closed() {
		return ErrAlreadyClosed
	}
	err := h.Start()
	if err != nil {
		return err
	}
	c, _ := h.clients.LoadOrStore(cli.u.ID, &clients{})
	c.lock.Lock()
	defer c.lock.Unlock()
	newC, loaded := h.clients.Load(cli.u.ID)
	if !loaded || c != newC {
		return h.RegClient(cli)
	}
	if c.m == nil {
		c.m = make(map[string]*Client)
	} else if _, ok := c.m[cli.ConnID()]; ok {
		return errors.New("client already exists")
	}
	c.m[cli.ConnID()] = cli
	return nil
}

func (h *Hub) UnRegClient(cli *Client) error {
	if h.Closed() {
		return ErrAlreadyClosed
	}
	if cli == nil {
		return errors.New("user is nil")
	}
	c, loaded := h.clients.Load(cli.u.ID)
	if !loaded {
		return errors.New("client not found")
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	if _, ok := c.m[cli.ConnID()]; !ok {
		return errors.New("client not found")
	}
	delete(c.m, cli.ConnID())
	if len(c.m) == 0 {
		h.clients.CompareAndDelete(cli.u.ID, c)
	}
	return nil
}

func (h *Hub) ClientNum() int64 {
	return h.clients.Len()
}

func (h *Hub) SendToUser(userID string, data Message) (err error) {
	if h.Closed() {
		return ErrAlreadyClosed
	}
	cli, ok := h.clients.Load(userID)
	if !ok {
		return nil
	}
	cli.lock.RLock()
	defer cli.lock.RUnlock()
	for _, c := range cli.m {
		if err = c.Send(data); err != nil {
			c.Close()
		}
	}
	return
}

func (h *Hub) SendToConnID(userID, connID string, data Message) error {
	cli, ok := h.GetClientByConnID(userID, connID)
	if !ok {
		return nil
	}
	return cli.Send(data)
}

func (h *Hub) GetClientByConnID(userID, connID string) (*Client, bool) {
	c, ok := h.clients.Load(userID)
	if !ok {
		return nil, false
	}
	client, ok := c.m[connID]
	return client, ok
}

func (h *Hub) IsOnline(userID string) bool {
	_, ok := h.clients.Load(userID)
	return ok
}

func (h *Hub) OnlineCount(userID string) int {
	c, ok := h.clients.Load(userID)
	if !ok {
		return 0
	}
	c.lock.RLock()
	defer c.lock.RUnlock()
	if len(c.m) == 0 {
		h.clients.CompareAndDelete(userID, c)
	}
	return len(c.m)
}

func (h *Hub) KickUser(userID string) error {
	if h.Closed() {
		return ErrAlreadyClosed
	}
	cli, ok := h.clients.Load(userID)
	if !ok {
		return nil
	}
	cli.lock.RLock()
	defer cli.lock.RUnlock()
	for _, c := range cli.m {
		c.Close()
	}
	return nil
}
