package providers

import (
	"context"
	"net/http"
	"strconv"

	json "github.com/json-iterator/go"
	"github.com/PeterChen1997/synctv/internal/provider"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

type GithubProvider struct {
	config oauth2.Config
}

func newGithubProvider() provider.Interface {
	return &GithubProvider{
		config: oauth2.Config{
			Scopes:   []string{"user"},
			Endpoint: github.Endpoint,
		},
	}
}

func (p *GithubProvider) Init(c provider.Oauth2Option) {
	p.config.ClientID = c.ClientID
	p.config.ClientSecret = c.ClientSecret
	p.config.RedirectURL = c.RedirectURL
}

func (p *GithubProvider) Provider() provider.OAuth2Provider {
	return "github"
}

func (p *GithubProvider) NewAuthURL(_ context.Context, state string) (string, error) {
	return p.config.AuthCodeURL(state, oauth2.AccessTypeOnline), nil
}

func (p *GithubProvider) GetToken(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.config.Exchange(ctx, code)
}

func (p *GithubProvider) RefreshToken(ctx context.Context, tk string) (*oauth2.Token, error) {
	return p.config.TokenSource(ctx, &oauth2.Token{RefreshToken: tk}).Token()
}

func (p *GithubProvider) GetUserInfo(ctx context.Context, code string) (*provider.UserInfo, error) {
	tk, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}
	client := p.config.Client(ctx, tk)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ui := githubUserInfo{}
	err = json.NewDecoder(resp.Body).Decode(&ui)
	if err != nil {
		return nil, err
	}
	return &provider.UserInfo{
		Username:       ui.Login,
		ProviderUserID: strconv.FormatUint(ui.ID, 10),
	}, nil
}

type githubUserInfo struct {
	Login string `json:"login"`
	ID    uint64 `json:"id"`
}

func init() {
	RegisterProvider(newGithubProvider())
}
