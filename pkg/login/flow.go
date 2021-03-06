package login

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dcos/dcos-cli/pkg/httpclient"
	"github.com/dcos/dcos-cli/pkg/open"
	"github.com/dcos/dcos-cli/pkg/prompt"
	"github.com/dgrijalva/jwt-go"
	"github.com/sirupsen/logrus"
)

// FlowOpts are functional options for a Flow.
type FlowOpts struct {
	Errout io.Writer
	Prompt *prompt.Prompt
	Logger *logrus.Logger
	Opener open.Opener
}

// Flow represents a login flow.
type Flow struct {
	client      *Client
	errout      io.Writer
	prompt      *prompt.Prompt
	logger      *logrus.Logger
	opener      open.Opener
	flags       *Flags
	interactive bool
}

// NewFlow creates a new login flow.
func NewFlow(opts FlowOpts) *Flow {
	if opts.Errout == nil {
		opts.Errout = os.Stderr
	}
	if opts.Prompt == nil {
		opts.Prompt = prompt.New(os.Stdin, os.Stdout)
	}
	if opts.Logger == nil {
		opts.Logger = &logrus.Logger{
			Out:   opts.Errout,
			Level: logrus.InfoLevel,
		}
	}
	if opts.Opener == nil {
		opts.Opener = open.NewOsOpener(opts.Logger)
	}
	return &Flow{
		errout: opts.Errout,
		prompt: opts.Prompt,
		logger: opts.Logger,
		opener: opts.Opener,
	}
}

// Start initiates the login flow for a given set of flags and HTTP client.
func (f *Flow) Start(flags *Flags, httpClient *httpclient.Client) (string, error) {
	if err := flags.Resolve(); err != nil {
		return "", err
	}
	f.flags = flags

	f.client = NewClient(httpClient, f.logger)
	providers, err := f.client.Providers()
	if err != nil {
		return "", err
	}

	provider, err := f.selectProvider(providers)
	if err != nil {
		return "", err
	}
	f.logger.Infof("Using login provider '%s'.", provider.Type)

	return f.triggerMethod(provider)
}

// SelectProvider explicitly, implicitly, or manually selects the provider to use for login.
func (f *Flow) selectProvider(providers Providers) (*Provider, error) {
	// Explicit provider selection.
	if f.flags.providerID != "" {
		provider, ok := providers[f.flags.providerID]
		if ok {
			return provider, nil
		}
		return nil, fmt.Errorf("unknown login provider ID '%s'", f.flags.providerID)
	}

	// Extract login provider candidates for implicit or manual selection.
	var providerCandidates []*Provider
	for _, provider := range providers.Slice() {
		if f.flags.Supports(provider) {
			providerCandidates = append(providerCandidates, provider)
		} else {
			f.logger.Infof("Excluding provider '%s' based on command-line flags.", provider.ID)
		}
	}

	switch len(providerCandidates) {
	case 0:
		return nil, errors.New("couldn't determine a login provider")
	case 1:
		// Implicit provider selection.
		return providerCandidates[0], nil
	default:
		// Manual provider selection.
		i, err := f.prompt.Select("Please select a login method:", providerCandidates)
		if err != nil {
			return nil, err
		}
		return providerCandidates[i], nil
	}
}

// TriggerMethod initiates the client login method of a given provider.
func (f *Flow) triggerMethod(provider *Provider) (string, error) {
	var loginEndpoint string
	credentials := &Credentials{}

	for attempt := 1; ; attempt++ {
		switch provider.ClientMethod {

		// Read UID and password from command-line flags or prompt for them.
		case methodCredential, methodUserCredential:
			loginEndpoint = provider.Config.StartFlowURL
			credentials.UID = f.uid()
			credentials.Password = f.password()

		// Read UID from the command-line flags and generate a service login
		// token from the --private-key. The token has a 5 minutes lifetime.
		case methodServiceCredential:
			credentials.UID = f.uid()
			token, err := f.serviceToken(credentials.UID)
			if err != nil {
				return "", err
			}
			credentials.Token = token

		// Open the browser at the `start_flow_url` location specified in the provider config.
		// The user is then expected to continue the flow in the browser and copy paste the
		// login token from the browser to their terminal.
		case methodBrowserToken:
			if attempt == 1 {
				if err := f.openBrowser(provider.Config.StartFlowURL); err != nil {
					return "", err
				}
			}
			f.interactive = true
			credentials.Token = f.prompt.Input("Enter token from the browser: ")
		}

		acsToken, err := f.client.Login(loginEndpoint, credentials)

		// In case of failure, let the user re-enter credentials 2 times.
		if err == nil || !f.interactive || attempt >= 3 {
			return acsToken, err
		}
	}
}

// uid returns the UID from the flag or prompts the user for it.
func (f *Flow) uid() string {
	if f.flags.username != "" {
		return f.flags.username
	}
	f.interactive = true
	return f.prompt.Input("Username: ")
}

// password returns the password from the resolved flag or prompts the user for it.
func (f *Flow) password() string {
	if f.flags.password != "" {
		return f.flags.password
	}
	f.interactive = true
	return f.prompt.Password("Password: ")
}

// serviceToken generates a login token based on a UID / service account private key.
func (f *Flow) serviceToken(uid string) (string, error) {
	return jwt.NewWithClaims(jwt.GetSigningMethod("RS256"), jwt.MapClaims{
		"uid": uid,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}).SignedString(f.flags.privateKey)
}

// openBrowser opens the browser at a given start flow URL.
func (f *Flow) openBrowser(startFlowURL string) error {
	// The start flow URL might be a relative or absolute URL.
	if strings.HasPrefix(startFlowURL, "/") {
		req, err := f.client.http.NewRequest("GET", startFlowURL, nil)
		if err != nil {
			return err
		}
		startFlowURL = req.URL.String()
	}
	if err := f.opener.Open(startFlowURL); err != nil {
		f.logger.Error(err)
	}
	msg := "If your browser didn't open, please follow this link:\n\n    %s\n\n"
	fmt.Fprintf(f.errout, msg, startFlowURL)
	return nil
}
