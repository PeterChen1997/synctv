package plugins

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/PeterChen1997/synctv/internal/provider"
	"github.com/PeterChen1997/synctv/internal/provider/providers"
	"github.com/PeterChen1997/synctv/internal/sysnotify"
	providerpb "github.com/PeterChen1997/synctv/proto/provider"
	"google.golang.org/grpc"
)

func InitProviderPlugins(name string, arg []string, logger hclog.Logger) error {
	client := NewProviderPlugin(name, arg, logger)
	err := sysnotify.RegisterSysNotifyTask(
		0,
		sysnotify.NewSysNotifyTask("plugin", sysnotify.NotifyTypeEXIT, func() error {
			client.Kill()
			return nil
		}),
	)
	if err != nil {
		return err
	}
	c, err := client.Client()
	if err != nil {
		return err
	}
	i, err := c.Dispense("Provider")
	if err != nil {
		return err
	}
	provider, ok := i.(provider.Interface)
	if !ok {
		return fmt.Errorf("%s not implement ProviderInterface", name)
	}
	providers.RegisterProvider(provider)
	return nil
}

var HandshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "BASIC_PLUGIN",
	MagicCookieValue: "hello",
}

var pluginMap = map[string]plugin.Plugin{
	"Provider": &ProviderPlugin{},
}

type ProviderPlugin struct {
	plugin.Plugin
	Impl provider.Interface
}

func (p *ProviderPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	providerpb.RegisterOauth2PluginServer(s, &GRPCServer{Impl: p.Impl})
	return nil
}

func (p *ProviderPlugin) GRPCClient(
	_ context.Context,
	_ *plugin.GRPCBroker,
	c *grpc.ClientConn,
) (any, error) {
	return &GRPCClient{client: providerpb.NewOauth2PluginClient(c)}, nil
}

func NewProviderPlugin(name string, arg []string, logger hclog.Logger) *plugin.Client {
	return plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins:         pluginMap,
		Cmd:             exec.Command(name, arg...),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
		},
		Logger: logger,
	})
}
