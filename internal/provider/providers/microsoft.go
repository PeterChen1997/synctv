package providers

import (
	"context"
	"net/http"

	json "github.com/json-iterator/go"
	"github.com/PeterChen1997/synctv/internal/provider"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

type MicrosoftProvider struct {
	config oauth2.Config
}

func newMicrosoftProvider() provider.Interface {
	return &MicrosoftProvider{
		config: oauth2.Config{
			Scopes:   []string{"user.read"},
			Endpoint: microsoft.LiveConnectEndpoint,
		},
	}
}

func (p *MicrosoftProvider) Init(c provider.Oauth2Option) {
	p.config.ClientID = c.ClientID
	p.config.ClientSecret = c.ClientSecret
	p.config.RedirectURL = c.RedirectURL
}

func (p *MicrosoftProvider) Provider() provider.OAuth2Provider {
	return "microsoft"
}

func (p *MicrosoftProvider) NewAuthURL(_ context.Context, state string) (string, error) {
	return p.config.AuthCodeURL(state, oauth2.AccessTypeOnline), nil
}

func (p *MicrosoftProvider) GetToken(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.config.Exchange(ctx, code)
}

func (p *MicrosoftProvider) RefreshToken(ctx context.Context, tk string) (*oauth2.Token, error) {
	return p.config.TokenSource(ctx, &oauth2.Token{RefreshToken: tk}).Token()
}

func (p *MicrosoftProvider) GetUserInfo(
	ctx context.Context,
	code string,
) (*provider.UserInfo, error) {
	tk, err := p.GetToken(ctx, code)
	if err != nil {
		return nil, err
	}
	client := p.config.Client(ctx, tk)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://graph.microsoft.com/v1.0/me",
		nil,
	)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ui := microsoftUserInfo{}
	err = json.NewDecoder(resp.Body).Decode(&ui)
	if err != nil {
		return nil, err
	}
	return &provider.UserInfo{
		Username:       ui.DisplayName,
		ProviderUserID: ui.ID,
	}, nil
}

type microsoftUserInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func init() {
	RegisterProvider(newMicrosoftProvider())
}
