package vendor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/aegis/circuitbreaker/sre"
	consul "github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/contrib/registry/etcd/v2"
	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/auth/jwt"
	kcircuitbreaker "github.com/go-kratos/kratos/v2/middleware/circuitbreaker"
	"github.com/go-kratos/kratos/v2/selector"
	"github.com/go-kratos/kratos/v2/selector/wrr"
	ggrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/hashicorp/consul/api"
	log "github.com/sirupsen/logrus"
	"github.com/PeterChen1997/synctv/internal/db"
	"github.com/PeterChen1997/synctv/internal/model"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
)

func init() {
	klog.SetLogger(klog.NewStdLogger(log.StandardLogger().Writer()))
	selector.SetGlobalSelector(wrr.NewBuilder())
}

type Backends struct {
	conns   map[string]*BackendConn
	clients *Clients
}

var (
	backends atomic.Pointer[Backends]
	lock     sync.Mutex
)

func LoadClients() *Clients {
	return backends.Load().clients
}

func storeBackends(conns map[string]*BackendConn, clients *Clients) {
	backends.Store(&Backends{
		conns:   conns,
		clients: clients,
	})
}

func LoadConns() map[string]*BackendConn {
	return backends.Load().conns
}

func Init(ctx context.Context) error {
	vb, err := db.GetAllVendorBackend()
	if err != nil {
		return err
	}
	bc, err := newBackendConns(ctx, vb)
	if err != nil {
		return err
	}
	vc, err := newVendorClients(bc)
	if err != nil {
		return err
	}
	storeBackends(bc, vc)
	return nil
}

func EnableVendorBackend(_ context.Context, endpoint string) (err error) {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	if v, ok := raw[endpoint]; !ok {
		return fmt.Errorf("endpoint not found: %s", endpoint)
	} else if v.Info.UsedBy.Enabled {
		return nil
	}

	raw[endpoint].Info.UsedBy.Enabled = true
	defer func() {
		if err != nil {
			raw[endpoint].Info.UsedBy.Enabled = false
		}
	}()

	vc, err := newVendorClients(raw)
	if err != nil {
		return err
	}

	err = db.EnableVendorBackend(endpoint)
	if err != nil {
		return err
	}

	storeBackends(raw, vc)

	return nil
}

func EnableVendorBackends(_ context.Context, endpoints []string) (err error) {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	needChangeEndpoints := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if v, ok := raw[endpoint]; !ok {
			return fmt.Errorf("endpoint not found: %s", endpoint)
		} else if !v.Info.UsedBy.Enabled {
			needChangeEndpoints = append(needChangeEndpoints, endpoint)
		}
	}

	for _, endpoint := range needChangeEndpoints {
		raw[endpoint].Info.UsedBy.Enabled = true
	}

	defer func() {
		if err != nil {
			for _, endpoint := range needChangeEndpoints {
				raw[endpoint].Info.UsedBy.Enabled = false
			}
		}
	}()

	vc, err := newVendorClients(raw)
	if err != nil {
		return err
	}

	err = db.EnableVendorBackends(needChangeEndpoints)
	if err != nil {
		return err
	}

	storeBackends(raw, vc)

	return nil
}

func DisableVendorBackend(_ context.Context, endpoint string) (err error) {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	if v, ok := raw[endpoint]; !ok {
		return fmt.Errorf("endpoint not found: %s", endpoint)
	} else if !v.Info.UsedBy.Enabled {
		return nil
	}

	raw[endpoint].Info.UsedBy.Enabled = false
	defer func() {
		if err != nil {
			raw[endpoint].Info.UsedBy.Enabled = true
		}
	}()

	vc, err := newVendorClients(raw)
	if err != nil {
		return err
	}

	err = db.DisableVendorBackend(endpoint)
	if err != nil {
		return err
	}

	storeBackends(raw, vc)

	return nil
}

func DisableVendorBackends(_ context.Context, endpoints []string) (err error) {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	needChangeEndpoints := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if v, ok := raw[endpoint]; !ok {
			return fmt.Errorf("endpoint not found: %s", endpoint)
		} else if v.Info.UsedBy.Enabled {
			needChangeEndpoints = append(needChangeEndpoints, endpoint)
		}
	}

	for _, endpoint := range needChangeEndpoints {
		raw[endpoint].Info.UsedBy.Enabled = false
	}

	defer func() {
		if err != nil {
			for _, endpoint := range needChangeEndpoints {
				raw[endpoint].Info.UsedBy.Enabled = true
			}
		}
	}()

	vc, err := newVendorClients(raw)
	if err != nil {
		return err
	}

	err = db.DisableVendorBackends(needChangeEndpoints)
	if err != nil {
		return err
	}

	storeBackends(raw, vc)

	return nil
}

func AddVendorBackend(ctx context.Context, backend *model.VendorBackend) error {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	if _, ok := raw[backend.Backend.Endpoint]; ok {
		return fmt.Errorf("duplicate endpoint: %s", backend.Backend.Endpoint)
	}

	bc, err := newBackendConn(ctx, backend)
	if err != nil {
		return err
	}

	m := maps.Clone(raw)
	m[backend.Backend.Endpoint] = bc

	vc, err := newVendorClients(m)
	if err != nil {
		bc.Conn.Close()
		return err
	}

	err = db.CreateVendorBackend(backend)
	if err != nil {
		bc.Conn.Close()
		return err
	}

	storeBackends(m, vc)

	return nil
}

func DeleteVendorBackend(_ context.Context, endpoint string) error {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	if _, ok := raw[endpoint]; !ok {
		return fmt.Errorf("endpoint not found: %s", endpoint)
	}

	m := maps.Clone(raw)
	beforeConn := m[endpoint].Conn
	delete(m, endpoint)

	vc, err := newVendorClients(m)
	if err != nil {
		return err
	}

	err = db.DeleteVendorBackend(endpoint)
	if err != nil {
		return err
	}

	storeBackends(m, vc)
	beforeConn.Close()

	return nil
}

func DeleteVendorBackends(_ context.Context, endpoints []string) error {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	m := maps.Clone(LoadConns())

	beforeConn := make([]*grpc.ClientConn, len(endpoints))
	for i, endpoint := range endpoints {
		conn, ok := m[endpoint]
		if !ok {
			return fmt.Errorf("endpoint not found: %s", endpoint)
		}
		beforeConn[i] = conn.Conn
		delete(m, endpoint)
	}

	vc, err := newVendorClients(m)
	if err != nil {
		return err
	}

	err = db.DeleteVendorBackends(endpoints)
	if err != nil {
		return err
	}

	storeBackends(m, vc)
	for _, conn := range beforeConn {
		conn.Close()
	}

	return nil
}

func UpdateVendorBackend(ctx context.Context, backend *model.VendorBackend) error {
	if !lock.TryLock() {
		return errors.New("vendor backend is updating")
	}
	defer lock.Unlock()

	raw := LoadConns()
	if _, ok := raw[backend.Backend.Endpoint]; !ok {
		return fmt.Errorf("endpoint not found: %s", backend.Backend.Endpoint)
	}

	bc, err := newBackendConn(ctx, backend)
	if err != nil {
		return err
	}

	m := maps.Clone(raw)
	beforeConn := m[backend.Backend.Endpoint].Conn
	m[backend.Backend.Endpoint] = bc

	vc, err := newVendorClients(m)
	if err != nil {
		bc.Conn.Close()
		return err
	}

	err = db.SaveVendorBackend(backend)
	if err != nil {
		bc.Conn.Close()
		return err
	}

	storeBackends(m, vc)
	beforeConn.Close()

	return nil
}

type BackendConn struct {
	Conn *grpc.ClientConn
	Info *model.VendorBackend
}

type Clients struct {
	bilibili map[string]BilibiliInterface
	alist    map[string]AlistInterface
	emby     map[string]EmbyInterface
}

func (b *Clients) BilibiliClients() map[string]BilibiliInterface {
	return b.bilibili
}

func (b *Clients) AlistClients() map[string]AlistInterface {
	return b.alist
}

func (b *Clients) EmbyClients() map[string]EmbyInterface {
	return b.emby
}

func newBackendConn(
	ctx context.Context,
	conf *model.VendorBackend,
) (conns *BackendConn, err error) {
	cc, err := NewGrpcConn(ctx, &conf.Backend)
	if err != nil {
		return conns, err
	}
	return &BackendConn{
		Conn: cc,
		Info: conf,
	}, nil
}

func newBackendConns(
	ctx context.Context,
	conf []*model.VendorBackend,
) (conns map[string]*BackendConn, err error) {
	conns = make(map[string]*BackendConn, len(conf))
	defer func() {
		if err != nil {
			for endpoint, conn := range conns {
				delete(conns, endpoint)
				conn.Conn.Close()
			}
		}
	}()
	for _, vb := range conf {
		if _, ok := conns[vb.Backend.Endpoint]; ok {
			return conns, fmt.Errorf("duplicate endpoint: %s", vb.Backend.Endpoint)
		}
		cc, err := newBackendConn(ctx, vb)
		if err != nil {
			return conns, err
		}
		conns[vb.Backend.Endpoint] = cc
	}

	return conns, nil
}

func newVendorClients(conns map[string]*BackendConn) (*Clients, error) {
	clients := &Clients{
		bilibili: make(map[string]BilibiliInterface),
		alist:    make(map[string]AlistInterface),
		emby:     make(map[string]EmbyInterface),
	}
	for _, conn := range conns {
		if !conn.Info.UsedBy.Enabled {
			continue
		}
		if conn.Info.UsedBy.Bilibili {
			if _, ok := clients.bilibili[conn.Info.UsedBy.BilibiliBackendName]; ok {
				return nil, fmt.Errorf(
					"duplicate bilibili backend name: %s",
					conn.Info.UsedBy.BilibiliBackendName,
				)
			}
			cli, err := NewBilibiliGrpcClient(conn.Conn)
			if err != nil {
				return nil, err
			}
			clients.bilibili[conn.Info.UsedBy.BilibiliBackendName] = cli
		}
		if conn.Info.UsedBy.Alist {
			if _, ok := clients.alist[conn.Info.UsedBy.AlistBackendName]; ok {
				return nil, fmt.Errorf(
					"duplicate alist backend name: %s",
					conn.Info.UsedBy.AlistBackendName,
				)
			}
			cli, err := NewAlistGrpcClient(conn.Conn)
			if err != nil {
				return nil, err
			}
			clients.alist[conn.Info.UsedBy.AlistBackendName] = cli
		}
		if conn.Info.UsedBy.Emby {
			if _, ok := clients.emby[conn.Info.UsedBy.EmbyBackendName]; ok {
				return nil, fmt.Errorf(
					"duplicate emby backend name: %s",
					conn.Info.UsedBy.EmbyBackendName,
				)
			}
			cli, err := NewEmbyGrpcClient(conn.Conn)
			if err != nil {
				return nil, err
			}
			clients.emby[conn.Info.UsedBy.EmbyBackendName] = cli
		}
	}

	return clients, nil
}

func NewGrpcConn(ctx context.Context, conf *model.Backend) (*grpc.ClientConn, error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	_, _, err := net.SplitHostPort(conf.Endpoint)
	if err != nil {
		if !strings.Contains(err.Error(), "missing port in address") {
			return nil, err
		}
		if conf.TLS {
			conf.Endpoint += ":443"
		} else {
			conf.Endpoint += ":80"
		}
	}
	middlewares := []middleware.Middleware{
		kcircuitbreaker.Client(
			kcircuitbreaker.WithCircuitBreaker(func() circuitbreaker.CircuitBreaker {
				return sre.NewBreaker(
					sre.WithRequest(25),
					sre.WithWindow(time.Second*15),
				)
			}),
		),
	}

	if conf.JwtSecret != "" {
		key := []byte(conf.JwtSecret)
		middlewares = append(middlewares, jwt.Client(func(_ *jwtv5.Token) (any, error) {
			return key, nil
		}, jwt.WithSigningMethod(jwtv5.SigningMethodHS256)))
	}

	opts := []ggrpc.ClientOption{
		ggrpc.WithMiddleware(middlewares...),
		// ggrpc.WithOptions(grpc.WithBlock()),
	}

	if conf.TimeOut != "" {
		timeout, err := time.ParseDuration(conf.TimeOut)
		if err != nil {
			return nil, err
		}
		opts = append(opts, ggrpc.WithTimeout(timeout))
	}

	switch {
	case conf.Consul.ServiceName != "":
		c := api.DefaultConfig()
		c.Address = conf.Endpoint
		c.Token = conf.Consul.Token
		c.PathPrefix = conf.Consul.PathPrefix
		c.Namespace = conf.Consul.Namespace
		c.Partition = conf.Consul.Partition
		client, err := api.NewClient(c)
		if err != nil {
			return nil, err
		}
		endpoint := "discovery:///" + conf.Consul.ServiceName
		dis := consul.New(client)
		opts = append(opts, ggrpc.WithEndpoint(endpoint), ggrpc.WithDiscovery(dis))
		log.Infof("new grpc client with consul: %s", conf.Endpoint)
	case conf.Etcd.ServiceName != "":
		endpoint := "discovery:///" + conf.Etcd.ServiceName
		cli, err := clientv3.New(clientv3.Config{
			Endpoints: []string{conf.Endpoint},
			Username:  conf.Etcd.Username,
			Password:  conf.Etcd.Password,
		})
		if err != nil {
			return nil, err
		}
		dis := etcd.New(cli)
		opts = append(opts, ggrpc.WithEndpoint(endpoint), ggrpc.WithDiscovery(dis))
		log.Infof("new grpc client with etcd: %v", conf.Endpoint)
	default:
		opts = append(opts, ggrpc.WithEndpoint(conf.Endpoint))
		log.Infof("new grpc client with endpoint: %s", conf.Endpoint)
	}

	var con *grpc.ClientConn
	if conf.TLS {
		var rootCAs *x509.CertPool
		rootCAs, err = x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if conf.CustomCa != "" {
			rootCAs.AppendCertsFromPEM([]byte(conf.CustomCa))
		}
		opts = append(opts, ggrpc.WithTLSConfig(&tls.Config{
			RootCAs:    rootCAs,
			MinVersion: tls.VersionTLS12,
		}))

		con, err = ggrpc.Dial(
			ctx,
			opts...,
		)
	} else {
		con, err = ggrpc.DialInsecure(
			ctx,
			opts...,
		)
	}
	if err != nil {
		return nil, err
	}
	return con, nil
}

func NewHTTPClientConn(ctx context.Context, conf *model.Backend) (*http.Client, error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	_, _, err := net.SplitHostPort(conf.Endpoint)
	if err != nil {
		if !strings.Contains(err.Error(), "missing port in address") {
			return nil, err
		}
		if conf.TLS {
			conf.Endpoint += ":443"
		} else {
			conf.Endpoint += ":80"
		}
	}
	middlewares := []middleware.Middleware{
		kcircuitbreaker.Client(
			kcircuitbreaker.WithCircuitBreaker(func() circuitbreaker.CircuitBreaker {
				return sre.NewBreaker(
					sre.WithRequest(25),
					sre.WithWindow(time.Second*15),
				)
			}),
		),
	}

	if conf.JwtSecret != "" {
		key := []byte(conf.JwtSecret)
		middlewares = append(middlewares, jwt.Client(func(_ *jwtv5.Token) (any, error) {
			return key, nil
		}, jwt.WithSigningMethod(jwtv5.SigningMethodHS256)))
	}

	opts := []http.ClientOption{
		http.WithMiddleware(middlewares...),
	}

	if conf.TimeOut != "" {
		timeout, err := time.ParseDuration(conf.TimeOut)
		if err != nil {
			return nil, err
		}
		opts = append(opts, http.WithTimeout(timeout))
	} else {
		opts = append(opts, http.WithTimeout(time.Second*10))
	}

	if conf.TLS {
		rootCAs, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if conf.CustomCa != "" {
			b, err := os.ReadFile(conf.CustomCa)
			if err != nil {
				return nil, err
			}
			rootCAs.AppendCertsFromPEM(b)
		}
		opts = append(opts, http.WithTLSConfig(&tls.Config{
			RootCAs:    rootCAs,
			MinVersion: tls.VersionTLS12,
		}))
	}

	switch {
	case conf.Consul.ServiceName != "":
		c := api.DefaultConfig()
		c.Address = conf.Endpoint
		c.Token = conf.Consul.Token
		c.PathPrefix = conf.Consul.PathPrefix
		c.Namespace = conf.Consul.Namespace
		c.Partition = conf.Consul.Partition
		client, err := api.NewClient(c)
		if err != nil {
			return nil, err
		}
		endpoint := "discovery:///" + conf.Consul.ServiceName
		dis := consul.New(client)
		opts = append(opts, http.WithEndpoint(endpoint), http.WithDiscovery(dis))
		log.Infof("new http client with consul: %s", conf.Endpoint)
	case conf.Etcd.ServiceName != "":
		endpoint := "discovery:///" + conf.Etcd.ServiceName
		cli, err := clientv3.New(clientv3.Config{
			Endpoints: []string{conf.Endpoint},
			Username:  conf.Etcd.Username,
			Password:  conf.Etcd.Password,
		})
		if err != nil {
			return nil, err
		}
		dis := etcd.New(cli)
		opts = append(opts, http.WithEndpoint(endpoint), http.WithDiscovery(dis))
		log.Infof("new http client with etcd: %v", conf.Endpoint)
	default:
		opts = append(opts, http.WithEndpoint(conf.Endpoint))
		log.Infof("new http client with endpoint: %s", conf.Endpoint)
	}

	con, err := http.NewClient(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, err
	}
	return con, nil
}
