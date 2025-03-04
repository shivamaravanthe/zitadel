package command

import (
	"net/http"
	"reflect"
	"time"

	"github.com/zitadel/logging"
	"github.com/zitadel/oidc/v2/pkg/client/rp"
	"golang.org/x/oauth2"

	"github.com/zitadel/zitadel/internal/crypto"
	"github.com/zitadel/zitadel/internal/domain"
	"github.com/zitadel/zitadel/internal/errors"
	"github.com/zitadel/zitadel/internal/eventstore"
	providers "github.com/zitadel/zitadel/internal/idp"
	"github.com/zitadel/zitadel/internal/idp/providers/azuread"
	"github.com/zitadel/zitadel/internal/idp/providers/github"
	"github.com/zitadel/zitadel/internal/idp/providers/gitlab"
	"github.com/zitadel/zitadel/internal/idp/providers/google"
	"github.com/zitadel/zitadel/internal/idp/providers/jwt"
	"github.com/zitadel/zitadel/internal/idp/providers/ldap"
	"github.com/zitadel/zitadel/internal/idp/providers/oauth"
	"github.com/zitadel/zitadel/internal/idp/providers/oidc"
	"github.com/zitadel/zitadel/internal/repository/idp"
	"github.com/zitadel/zitadel/internal/repository/idpconfig"
	"github.com/zitadel/zitadel/internal/repository/instance"
	"github.com/zitadel/zitadel/internal/repository/org"
)

type OAuthIDPWriteModel struct {
	eventstore.WriteModel

	Name                  string
	ID                    string
	ClientID              string
	ClientSecret          *crypto.CryptoValue
	AuthorizationEndpoint string
	TokenEndpoint         string
	UserEndpoint          string
	Scopes                []string
	IDAttribute           string
	idp.Options

	State domain.IDPState
}

func (wm *OAuthIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.OAuthIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.OAuthIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *OAuthIDPWriteModel) reduceAddedEvent(e *idp.OAuthIDPAddedEvent) {
	wm.Name = e.Name
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.AuthorizationEndpoint = e.AuthorizationEndpoint
	wm.TokenEndpoint = e.TokenEndpoint
	wm.UserEndpoint = e.UserEndpoint
	wm.Scopes = e.Scopes
	wm.IDAttribute = e.IDAttribute
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *OAuthIDPWriteModel) reduceChangedEvent(e *idp.OAuthIDPChangedEvent) {
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.AuthorizationEndpoint != nil {
		wm.AuthorizationEndpoint = *e.AuthorizationEndpoint
	}
	if e.TokenEndpoint != nil {
		wm.TokenEndpoint = *e.TokenEndpoint
	}
	if e.UserEndpoint != nil {
		wm.UserEndpoint = *e.UserEndpoint
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	if e.IDAttribute != nil {
		wm.IDAttribute = *e.IDAttribute
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *OAuthIDPWriteModel) NewChanges(
	name,
	clientID,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	authorizationEndpoint,
	tokenEndpoint,
	userEndpoint,
	idAttribute string,
	scopes []string,
	options idp.Options,
) ([]idp.OAuthIDPChanges, error) {
	changes := make([]idp.OAuthIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeOAuthClientSecret(clientSecret))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeOAuthClientID(clientID))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeOAuthName(name))
	}
	if wm.AuthorizationEndpoint != authorizationEndpoint {
		changes = append(changes, idp.ChangeOAuthAuthorizationEndpoint(authorizationEndpoint))
	}
	if wm.TokenEndpoint != tokenEndpoint {
		changes = append(changes, idp.ChangeOAuthTokenEndpoint(tokenEndpoint))
	}
	if wm.UserEndpoint != userEndpoint {
		changes = append(changes, idp.ChangeOAuthUserEndpoint(userEndpoint))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeOAuthScopes(scopes))
	}
	if wm.IDAttribute != idAttribute {
		changes = append(changes, idp.ChangeOAuthIDAttribute(idAttribute))
	}
	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeOAuthOptions(opts))
	}
	return changes, nil
}

func (wm *OAuthIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	config := &oauth2.Config{
		ClientID:     wm.ClientID,
		ClientSecret: secret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  wm.AuthorizationEndpoint,
			TokenURL: wm.TokenEndpoint,
		},
		RedirectURL: callbackURL,
		Scopes:      wm.Scopes,
	}
	opts := make([]oauth.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		opts = append(opts, oauth.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, oauth.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, oauth.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, oauth.WithAutoUpdate())
	}
	return oauth.New(
		config,
		wm.Name,
		wm.UserEndpoint,
		func() providers.User {
			return oauth.NewUserMapper(wm.IDAttribute)
		},
		opts...,
	)
}

type OIDCIDPWriteModel struct {
	eventstore.WriteModel

	Name             string
	ID               string
	Issuer           string
	ClientID         string
	ClientSecret     *crypto.CryptoValue
	Scopes           []string
	IsIDTokenMapping bool
	idp.Options

	State domain.IDPState
}

func (wm *OIDCIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.OIDCIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.OIDCIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.OIDCIDPMigratedAzureADEvent:
			wm.State = domain.IDPStateMigrated
		case *idp.OIDCIDPMigratedGoogleEvent:
			wm.State = domain.IDPStateMigrated
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		case *idpconfig.IDPConfigAddedEvent:
			wm.reduceIDPConfigAddedEvent(e)
		case *idpconfig.IDPConfigChangedEvent:
			wm.reduceIDPConfigChangedEvent(e)
		case *idpconfig.OIDCConfigAddedEvent:
			wm.reduceOIDCConfigAddedEvent(e)
		case *idpconfig.OIDCConfigChangedEvent:
			wm.reduceOIDCConfigChangedEvent(e)
		case *idpconfig.IDPConfigRemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *OIDCIDPWriteModel) reduceAddedEvent(e *idp.OIDCIDPAddedEvent) {
	wm.Name = e.Name
	wm.Issuer = e.Issuer
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
	wm.IsIDTokenMapping = e.IsIDTokenMapping
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *OIDCIDPWriteModel) reduceChangedEvent(e *idp.OIDCIDPChangedEvent) {
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.Issuer != nil {
		wm.Issuer = *e.Issuer
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	if e.IsIDTokenMapping != nil {
		wm.IsIDTokenMapping = *e.IsIDTokenMapping
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *OIDCIDPWriteModel) NewChanges(
	name,
	issuer,
	clientID,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	scopes []string,
	idTokenMapping bool,
	options idp.Options,
) ([]idp.OIDCIDPChanges, error) {
	changes := make([]idp.OIDCIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeOIDCClientSecret(clientSecret))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeOIDCClientID(clientID))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeOIDCName(name))
	}
	if wm.Issuer != issuer {
		changes = append(changes, idp.ChangeOIDCIssuer(issuer))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeOIDCScopes(scopes))
	}
	if wm.IsIDTokenMapping != idTokenMapping {
		changes = append(changes, idp.ChangeOIDCIsIDTokenMapping(idTokenMapping))
	}
	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeOIDCOptions(opts))
	}
	return changes, nil
}

// reduceIDPConfigAddedEvent handles old idpConfig events
func (wm *OIDCIDPWriteModel) reduceIDPConfigAddedEvent(e *idpconfig.IDPConfigAddedEvent) {
	wm.Name = e.Name
	wm.Options.IsAutoCreation = e.AutoRegister
	wm.State = domain.IDPStateActive
}

// reduceIDPConfigChangedEvent handles old idpConfig changes
func (wm *OIDCIDPWriteModel) reduceIDPConfigChangedEvent(e *idpconfig.IDPConfigChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.AutoRegister != nil {
		wm.Options.IsAutoCreation = *e.AutoRegister
	}
}

// reduceOIDCConfigAddedEvent handles old OIDC idpConfig events
func (wm *OIDCIDPWriteModel) reduceOIDCConfigAddedEvent(e *idpconfig.OIDCConfigAddedEvent) {
	wm.Issuer = e.Issuer
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
}

// reduceOIDCConfigChangedEvent handles old OIDC idpConfig changes
func (wm *OIDCIDPWriteModel) reduceOIDCConfigChangedEvent(e *idpconfig.OIDCConfigChangedEvent) {
	if e.Issuer != nil {
		wm.Issuer = *e.Issuer
	}
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
}

func (wm *OIDCIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	opts := make([]oidc.ProviderOpts, 1, 6)
	opts[0] = oidc.WithSelectAccount()
	if wm.IsIDTokenMapping {
		opts = append(opts, oidc.WithIDTokenMapping())
	}
	if wm.IsCreationAllowed {
		opts = append(opts, oidc.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, oidc.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, oidc.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, oidc.WithAutoUpdate())
	}
	return oidc.New(
		wm.Name,
		wm.Issuer,
		wm.ClientID,
		secret,
		callbackURL,
		wm.Scopes,
		oidc.DefaultMapper,
		opts...,
	)
}

type JWTIDPWriteModel struct {
	eventstore.WriteModel

	ID           string
	Name         string
	Issuer       string
	JWTEndpoint  string
	KeysEndpoint string
	HeaderName   string
	idp.Options

	State domain.IDPState
}

func (wm *JWTIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.JWTIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.JWTIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		case *idpconfig.IDPConfigAddedEvent:
			wm.reduceIDPConfigAddedEvent(e)
		case *idpconfig.IDPConfigChangedEvent:
			wm.reduceIDPConfigChangedEvent(e)
		case *idpconfig.JWTConfigAddedEvent:
			wm.reduceJWTConfigAddedEvent(e)
		case *idpconfig.JWTConfigChangedEvent:
			wm.reduceJWTConfigChangedEvent(e)
		case *idpconfig.IDPConfigRemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *JWTIDPWriteModel) reduceAddedEvent(e *idp.JWTIDPAddedEvent) {
	wm.Name = e.Name
	wm.Issuer = e.Issuer
	wm.JWTEndpoint = e.JWTEndpoint
	wm.KeysEndpoint = e.KeysEndpoint
	wm.HeaderName = e.HeaderName
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *JWTIDPWriteModel) reduceChangedEvent(e *idp.JWTIDPChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.Issuer != nil {
		wm.Issuer = *e.Issuer
	}
	if e.JWTEndpoint != nil {
		wm.JWTEndpoint = *e.JWTEndpoint
	}
	if e.KeysEndpoint != nil {
		wm.KeysEndpoint = *e.KeysEndpoint
	}
	if e.HeaderName != nil {
		wm.HeaderName = *e.HeaderName
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *JWTIDPWriteModel) NewChanges(
	name,
	issuer,
	jwtEndpoint,
	keysEndpoint,
	headerName string,
	options idp.Options,
) ([]idp.JWTIDPChanges, error) {
	changes := make([]idp.JWTIDPChanges, 0)
	if wm.Name != name {
		changes = append(changes, idp.ChangeJWTName(name))
	}
	if wm.Issuer != issuer {
		changes = append(changes, idp.ChangeJWTIssuer(issuer))
	}
	if wm.JWTEndpoint != jwtEndpoint {
		changes = append(changes, idp.ChangeJWTEndpoint(jwtEndpoint))
	}
	if wm.KeysEndpoint != keysEndpoint {
		changes = append(changes, idp.ChangeJWTKeysEndpoint(keysEndpoint))
	}
	if wm.HeaderName != headerName {
		changes = append(changes, idp.ChangeJWTHeaderName(headerName))
	}
	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeJWTOptions(opts))
	}
	return changes, nil
}

// reduceIDPConfigAddedEvent handles old idpConfig events
func (wm *JWTIDPWriteModel) reduceIDPConfigAddedEvent(e *idpconfig.IDPConfigAddedEvent) {
	wm.Name = e.Name
	wm.Options.IsAutoCreation = e.AutoRegister
	wm.State = domain.IDPStateActive
}

// reduceIDPConfigChangedEvent handles old idpConfig changes
func (wm *JWTIDPWriteModel) reduceIDPConfigChangedEvent(e *idpconfig.IDPConfigChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.AutoRegister != nil {
		wm.Options.IsAutoCreation = *e.AutoRegister
	}
}

// reduceJWTConfigAddedEvent handles old JWT idpConfig events
func (wm *JWTIDPWriteModel) reduceJWTConfigAddedEvent(e *idpconfig.JWTConfigAddedEvent) {
	wm.Issuer = e.Issuer
	wm.JWTEndpoint = e.JWTEndpoint
	wm.KeysEndpoint = e.KeysEndpoint
	wm.HeaderName = e.HeaderName
}

// reduceJWTConfigChangedEvent handles old JWT idpConfig changes
func (wm *JWTIDPWriteModel) reduceJWTConfigChangedEvent(e *idpconfig.JWTConfigChangedEvent) {
	if e.Issuer != nil {
		wm.Issuer = *e.Issuer
	}
	if e.JWTEndpoint != nil {
		wm.JWTEndpoint = *e.JWTEndpoint
	}
	if e.KeysEndpoint != nil {
		wm.KeysEndpoint = *e.KeysEndpoint
	}
	if e.HeaderName != nil {
		wm.HeaderName = *e.HeaderName
	}
}

func (wm *JWTIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	opts := make([]jwt.ProviderOpts, 0)
	if wm.IsCreationAllowed {
		opts = append(opts, jwt.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, jwt.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, jwt.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, jwt.WithAutoUpdate())
	}
	return jwt.New(
		wm.Name,
		wm.Issuer,
		wm.JWTEndpoint,
		wm.KeysEndpoint,
		wm.HeaderName,
		idpAlg,
		opts...,
	)
}

type AzureADIDPWriteModel struct {
	eventstore.WriteModel

	ID              string
	Name            string
	ClientID        string
	ClientSecret    *crypto.CryptoValue
	Scopes          []string
	Tenant          string
	IsEmailVerified bool
	idp.Options

	State domain.IDPState
}

func (wm *AzureADIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.AzureADIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.OIDCIDPMigratedAzureADEvent:
			wm.reduceAddedEvent(&e.AzureADIDPAddedEvent)
		case *idp.AzureADIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *AzureADIDPWriteModel) reduceAddedEvent(e *idp.AzureADIDPAddedEvent) {
	wm.Name = e.Name
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
	wm.Tenant = e.Tenant
	wm.IsEmailVerified = e.IsEmailVerified
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *AzureADIDPWriteModel) reduceChangedEvent(e *idp.AzureADIDPChangedEvent) {
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	if e.Tenant != nil {
		wm.Tenant = *e.Tenant
	}
	if e.IsEmailVerified != nil {
		wm.IsEmailVerified = *e.IsEmailVerified
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *AzureADIDPWriteModel) NewChanges(
	name string,
	clientID string,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	scopes []string,
	tenant string,
	isEmailVerified bool,
	options idp.Options,
) ([]idp.AzureADIDPChanges, error) {
	changes := make([]idp.AzureADIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeAzureADClientSecret(clientSecret))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeAzureADName(name))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeAzureADClientID(clientID))
	}
	if wm.Tenant != tenant {
		changes = append(changes, idp.ChangeAzureADTenant(tenant))
	}
	if wm.IsEmailVerified != isEmailVerified {
		changes = append(changes, idp.ChangeAzureADIsEmailVerified(isEmailVerified))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeAzureADScopes(scopes))
	}

	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeAzureADOptions(opts))
	}
	return changes, nil
}
func (wm *AzureADIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	opts := make([]azuread.ProviderOptions, 0, 3)
	if wm.IsEmailVerified {
		opts = append(opts, azuread.WithEmailVerified())
	}
	if wm.Tenant != "" {
		opts = append(opts, azuread.WithTenant(azuread.TenantType(wm.Tenant)))
	}
	oauthOpts := make([]oauth.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		oauthOpts = append(oauthOpts, oauth.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		oauthOpts = append(oauthOpts, oauth.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		oauthOpts = append(oauthOpts, oauth.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		oauthOpts = append(oauthOpts, oauth.WithAutoUpdate())
	}
	if len(oauthOpts) > 0 {
		opts = append(opts, azuread.WithOAuthOptions(oauthOpts...))
	}
	return azuread.New(
		wm.Name,
		wm.ClientID,
		secret,
		callbackURL,
		wm.Scopes,
		opts...,
	)
}

type GitHubIDPWriteModel struct {
	eventstore.WriteModel

	ID           string
	Name         string
	ClientID     string
	ClientSecret *crypto.CryptoValue
	Scopes       []string
	idp.Options

	State domain.IDPState
}

func (wm *GitHubIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.GitHubIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.GitHubIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *GitHubIDPWriteModel) reduceAddedEvent(e *idp.GitHubIDPAddedEvent) {
	wm.Name = e.Name
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *GitHubIDPWriteModel) reduceChangedEvent(e *idp.GitHubIDPChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *GitHubIDPWriteModel) NewChanges(
	name,
	clientID,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	scopes []string,
	options idp.Options,
) ([]idp.GitHubIDPChanges, error) {
	changes := make([]idp.GitHubIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeGitHubClientSecret(clientSecret))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeGitHubName(name))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeGitHubClientID(clientID))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeGitHubScopes(scopes))
	}

	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeGitHubOptions(opts))
	}
	return changes, nil
}
func (wm *GitHubIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	oauthOpts := make([]oauth.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		oauthOpts = append(oauthOpts, oauth.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		oauthOpts = append(oauthOpts, oauth.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		oauthOpts = append(oauthOpts, oauth.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		oauthOpts = append(oauthOpts, oauth.WithAutoUpdate())
	}
	return github.New(
		wm.ClientID,
		secret,
		callbackURL,
		wm.Scopes,
		oauthOpts...,
	)
}

type GitHubEnterpriseIDPWriteModel struct {
	eventstore.WriteModel

	ID                    string
	Name                  string
	ClientID              string
	ClientSecret          *crypto.CryptoValue
	AuthorizationEndpoint string
	TokenEndpoint         string
	UserEndpoint          string
	Scopes                []string
	idp.Options

	State domain.IDPState
}

func (wm *GitHubEnterpriseIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.GitHubEnterpriseIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.GitHubEnterpriseIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *GitHubEnterpriseIDPWriteModel) reduceAddedEvent(e *idp.GitHubEnterpriseIDPAddedEvent) {
	wm.Name = e.Name
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.AuthorizationEndpoint = e.AuthorizationEndpoint
	wm.TokenEndpoint = e.TokenEndpoint
	wm.UserEndpoint = e.UserEndpoint
	wm.Scopes = e.Scopes
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *GitHubEnterpriseIDPWriteModel) reduceChangedEvent(e *idp.GitHubEnterpriseIDPChangedEvent) {
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.AuthorizationEndpoint != nil {
		wm.AuthorizationEndpoint = *e.AuthorizationEndpoint
	}
	if e.TokenEndpoint != nil {
		wm.TokenEndpoint = *e.TokenEndpoint
	}
	if e.UserEndpoint != nil {
		wm.UserEndpoint = *e.UserEndpoint
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *GitHubEnterpriseIDPWriteModel) NewChanges(
	name,
	clientID string,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	authorizationEndpoint,
	tokenEndpoint,
	userEndpoint string,
	scopes []string,
	options idp.Options,
) ([]idp.GitHubEnterpriseIDPChanges, error) {
	changes := make([]idp.GitHubEnterpriseIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeGitHubEnterpriseClientSecret(clientSecret))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeGitHubEnterpriseClientID(clientID))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeGitHubEnterpriseName(name))
	}
	if wm.AuthorizationEndpoint != authorizationEndpoint {
		changes = append(changes, idp.ChangeGitHubEnterpriseAuthorizationEndpoint(authorizationEndpoint))
	}
	if wm.TokenEndpoint != tokenEndpoint {
		changes = append(changes, idp.ChangeGitHubEnterpriseTokenEndpoint(tokenEndpoint))
	}
	if wm.UserEndpoint != userEndpoint {
		changes = append(changes, idp.ChangeGitHubEnterpriseUserEndpoint(userEndpoint))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeGitHubEnterpriseScopes(scopes))
	}
	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeGitHubEnterpriseOptions(opts))
	}
	return changes, nil
}

func (wm *GitHubEnterpriseIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	oauthOpts := make([]oauth.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		oauthOpts = append(oauthOpts, oauth.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		oauthOpts = append(oauthOpts, oauth.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		oauthOpts = append(oauthOpts, oauth.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		oauthOpts = append(oauthOpts, oauth.WithAutoUpdate())
	}
	return github.NewCustomURL(
		wm.Name,
		wm.ClientID,
		secret,
		callbackURL,
		wm.AuthorizationEndpoint,
		wm.TokenEndpoint,
		wm.UserEndpoint,
		wm.Scopes,
		oauthOpts...,
	)
}

type GitLabIDPWriteModel struct {
	eventstore.WriteModel

	ID           string
	Name         string
	ClientID     string
	ClientSecret *crypto.CryptoValue
	Scopes       []string
	idp.Options

	State domain.IDPState
}

func (wm *GitLabIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.GitLabIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.GitLabIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *GitLabIDPWriteModel) reduceAddedEvent(e *idp.GitLabIDPAddedEvent) {
	wm.Name = e.Name
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *GitLabIDPWriteModel) reduceChangedEvent(e *idp.GitLabIDPChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *GitLabIDPWriteModel) NewChanges(
	name,
	clientID,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	scopes []string,
	options idp.Options,
) ([]idp.GitLabIDPChanges, error) {
	changes := make([]idp.GitLabIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeGitLabClientSecret(clientSecret))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeGitLabName(name))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeGitLabClientID(clientID))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeGitLabScopes(scopes))
	}

	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeGitLabOptions(opts))
	}
	return changes, nil
}

func (wm *GitLabIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	opts := make([]oidc.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		opts = append(opts, oidc.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, oidc.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, oidc.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, oidc.WithAutoUpdate())
	}
	return gitlab.New(
		wm.ClientID,
		secret,
		callbackURL,
		wm.Scopes,
		opts...,
	)
}

type GitLabSelfHostedIDPWriteModel struct {
	eventstore.WriteModel

	ID           string
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret *crypto.CryptoValue
	Scopes       []string
	idp.Options

	State domain.IDPState
}

func (wm *GitLabSelfHostedIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.GitLabSelfHostedIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.GitLabSelfHostedIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *GitLabSelfHostedIDPWriteModel) reduceAddedEvent(e *idp.GitLabSelfHostedIDPAddedEvent) {
	wm.Name = e.Name
	wm.Issuer = e.Issuer
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *GitLabSelfHostedIDPWriteModel) reduceChangedEvent(e *idp.GitLabSelfHostedIDPChangedEvent) {
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.Issuer != nil {
		wm.Issuer = *e.Issuer
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *GitLabSelfHostedIDPWriteModel) NewChanges(
	name string,
	issuer string,
	clientID string,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	scopes []string,
	options idp.Options,
) ([]idp.GitLabSelfHostedIDPChanges, error) {
	changes := make([]idp.GitLabSelfHostedIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeGitLabSelfHostedClientSecret(clientSecret))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeGitLabSelfHostedClientID(clientID))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeGitLabSelfHostedName(name))
	}
	if wm.Issuer != issuer {
		changes = append(changes, idp.ChangeGitLabSelfHostedIssuer(issuer))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeGitLabSelfHostedScopes(scopes))
	}
	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeGitLabSelfHostedOptions(opts))
	}
	return changes, nil
}

func (wm *GitLabSelfHostedIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	opts := make([]oidc.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		opts = append(opts, oidc.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, oidc.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, oidc.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, oidc.WithAutoUpdate())
	}
	return gitlab.NewCustomIssuer(
		wm.Name,
		wm.Issuer,
		wm.ClientID,
		secret,
		callbackURL,
		wm.Scopes,
		opts...,
	)
}

type GoogleIDPWriteModel struct {
	eventstore.WriteModel

	ID           string
	Name         string
	ClientID     string
	ClientSecret *crypto.CryptoValue
	Scopes       []string
	idp.Options

	State domain.IDPState
}

func (wm *GoogleIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.GoogleIDPAddedEvent:
			wm.reduceAddedEvent(e)
		case *idp.GoogleIDPChangedEvent:
			wm.reduceChangedEvent(e)
		case *idp.OIDCIDPMigratedGoogleEvent:
			wm.reduceAddedEvent(&e.GoogleIDPAddedEvent)
		case *idp.RemovedEvent:
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *GoogleIDPWriteModel) reduceAddedEvent(e *idp.GoogleIDPAddedEvent) {
	wm.Name = e.Name
	wm.ClientID = e.ClientID
	wm.ClientSecret = e.ClientSecret
	wm.Scopes = e.Scopes
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *GoogleIDPWriteModel) reduceChangedEvent(e *idp.GoogleIDPChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.ClientID != nil {
		wm.ClientID = *e.ClientID
	}
	if e.ClientSecret != nil {
		wm.ClientSecret = e.ClientSecret
	}
	if e.Scopes != nil {
		wm.Scopes = e.Scopes
	}
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *GoogleIDPWriteModel) NewChanges(
	name string,
	clientID string,
	clientSecretString string,
	secretCrypto crypto.Crypto,
	scopes []string,
	options idp.Options,
) ([]idp.GoogleIDPChanges, error) {
	changes := make([]idp.GoogleIDPChanges, 0)
	var clientSecret *crypto.CryptoValue
	var err error
	if clientSecretString != "" {
		clientSecret, err = crypto.Crypt([]byte(clientSecretString), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeGoogleClientSecret(clientSecret))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeGoogleName(name))
	}
	if wm.ClientID != clientID {
		changes = append(changes, idp.ChangeGoogleClientID(clientID))
	}
	if !reflect.DeepEqual(wm.Scopes, scopes) {
		changes = append(changes, idp.ChangeGoogleScopes(scopes))
	}

	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeGoogleOptions(opts))
	}
	return changes, nil
}

func (wm *GoogleIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	errorHandler := func(w http.ResponseWriter, r *http.Request, errorType string, errorDesc string, state string) {
		logging.Errorf("token exchanged failed: %s - %s (state: %s)", errorType, errorType, state)
		rp.DefaultErrorHandler(w, r, errorType, errorDesc, state)
	}
	oidc.WithRelyingPartyOption(rp.WithErrorHandler(errorHandler))
	secret, err := crypto.DecryptString(wm.ClientSecret, idpAlg)
	if err != nil {
		return nil, err
	}
	opts := make([]oidc.ProviderOpts, 0, 4)
	if wm.IsCreationAllowed {
		opts = append(opts, oidc.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, oidc.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, oidc.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, oidc.WithAutoUpdate())
	}
	return google.New(
		wm.ClientID,
		secret,
		callbackURL,
		wm.Scopes,
		opts...,
	)
}

type LDAPIDPWriteModel struct {
	eventstore.WriteModel

	ID                string
	Name              string
	Servers           []string
	StartTLS          bool
	BaseDN            string
	BindDN            string
	BindPassword      *crypto.CryptoValue
	UserBase          string
	UserObjectClasses []string
	UserFilters       []string
	Timeout           time.Duration
	idp.LDAPAttributes
	idp.Options

	State domain.IDPState
}

func (wm *LDAPIDPWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.LDAPIDPAddedEvent:
			if wm.ID != e.ID {
				continue
			}
			wm.reduceAddedEvent(e)
		case *idp.LDAPIDPChangedEvent:
			if wm.ID != e.ID {
				continue
			}
			wm.reduceChangedEvent(e)
		case *idp.RemovedEvent:
			if wm.ID != e.ID {
				continue
			}
			wm.State = domain.IDPStateRemoved
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *LDAPIDPWriteModel) reduceAddedEvent(e *idp.LDAPIDPAddedEvent) {
	wm.Name = e.Name
	wm.Servers = e.Servers
	wm.StartTLS = e.StartTLS
	wm.BaseDN = e.BaseDN
	wm.BindDN = e.BindDN
	wm.BindPassword = e.BindPassword
	wm.UserBase = e.UserBase
	wm.UserObjectClasses = e.UserObjectClasses
	wm.UserFilters = e.UserFilters
	wm.Timeout = e.Timeout
	wm.LDAPAttributes = e.LDAPAttributes
	wm.Options = e.Options
	wm.State = domain.IDPStateActive
}

func (wm *LDAPIDPWriteModel) reduceChangedEvent(e *idp.LDAPIDPChangedEvent) {
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.Name != nil {
		wm.Name = *e.Name
	}
	if e.Servers != nil {
		wm.Servers = e.Servers
	}
	if e.StartTLS != nil {
		wm.StartTLS = *e.StartTLS
	}
	if e.BaseDN != nil {
		wm.BaseDN = *e.BaseDN
	}
	if e.BindDN != nil {
		wm.BindDN = *e.BindDN
	}
	if e.BindPassword != nil {
		wm.BindPassword = e.BindPassword
	}
	if e.UserBase != nil {
		wm.UserBase = *e.UserBase
	}
	if e.UserObjectClasses != nil {
		wm.UserObjectClasses = e.UserObjectClasses
	}
	if e.UserFilters != nil {
		wm.UserFilters = e.UserFilters
	}
	if e.Timeout != nil {
		wm.Timeout = *e.Timeout
	}
	wm.LDAPAttributes.ReduceChanges(e.LDAPAttributeChanges)
	wm.Options.ReduceChanges(e.OptionChanges)
}

func (wm *LDAPIDPWriteModel) NewChanges(
	name string,
	servers []string,
	startTLS bool,
	baseDN string,
	bindDN string,
	bindPassword string,
	userBase string,
	userObjectClasses []string,
	userFilters []string,
	timeout time.Duration,
	secretCrypto crypto.Crypto,
	attributes idp.LDAPAttributes,
	options idp.Options,
) ([]idp.LDAPIDPChanges, error) {
	changes := make([]idp.LDAPIDPChanges, 0)
	var cryptedPassword *crypto.CryptoValue
	var err error
	if bindPassword != "" {
		cryptedPassword, err = crypto.Crypt([]byte(bindPassword), secretCrypto)
		if err != nil {
			return nil, err
		}
		changes = append(changes, idp.ChangeLDAPBindPassword(cryptedPassword))
	}
	if wm.Name != name {
		changes = append(changes, idp.ChangeLDAPName(name))
	}
	if !reflect.DeepEqual(wm.Servers, servers) {
		changes = append(changes, idp.ChangeLDAPServers(servers))
	}
	if wm.StartTLS != startTLS {
		changes = append(changes, idp.ChangeLDAPStartTLS(startTLS))
	}
	if wm.BaseDN != baseDN {
		changes = append(changes, idp.ChangeLDAPBaseDN(baseDN))
	}
	if wm.BindDN != bindDN {
		changes = append(changes, idp.ChangeLDAPBindDN(bindDN))
	}
	if wm.UserBase != userBase {
		changes = append(changes, idp.ChangeLDAPUserBase(userBase))
	}
	if !reflect.DeepEqual(wm.UserObjectClasses, userObjectClasses) {
		changes = append(changes, idp.ChangeLDAPUserObjectClasses(userObjectClasses))
	}
	if !reflect.DeepEqual(wm.UserFilters, userFilters) {
		changes = append(changes, idp.ChangeLDAPUserFilters(userFilters))
	}
	if wm.Timeout != timeout {
		changes = append(changes, idp.ChangeLDAPTimeout(timeout))
	}
	attrs := wm.LDAPAttributes.Changes(attributes)
	if !attrs.IsZero() {
		changes = append(changes, idp.ChangeLDAPAttributes(attrs))
	}
	opts := wm.Options.Changes(options)
	if !opts.IsZero() {
		changes = append(changes, idp.ChangeLDAPOptions(opts))
	}
	return changes, nil
}

func (wm *LDAPIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	password, err := crypto.DecryptString(wm.BindPassword, idpAlg)
	if err != nil {
		return nil, err
	}
	var opts []ldap.ProviderOpts
	if !wm.StartTLS {
		opts = append(opts, ldap.WithoutStartTLS())
	}
	if wm.LDAPAttributes.IDAttribute != "" {
		opts = append(opts, ldap.WithCustomIDAttribute(wm.LDAPAttributes.IDAttribute))
	}
	if wm.LDAPAttributes.FirstNameAttribute != "" {
		opts = append(opts, ldap.WithFirstNameAttribute(wm.LDAPAttributes.FirstNameAttribute))
	}
	if wm.LDAPAttributes.LastNameAttribute != "" {
		opts = append(opts, ldap.WithLastNameAttribute(wm.LDAPAttributes.LastNameAttribute))
	}
	if wm.LDAPAttributes.DisplayNameAttribute != "" {
		opts = append(opts, ldap.WithDisplayNameAttribute(wm.LDAPAttributes.DisplayNameAttribute))
	}
	if wm.LDAPAttributes.NickNameAttribute != "" {
		opts = append(opts, ldap.WithNickNameAttribute(wm.LDAPAttributes.NickNameAttribute))
	}
	if wm.LDAPAttributes.PreferredUsernameAttribute != "" {
		opts = append(opts, ldap.WithPreferredUsernameAttribute(wm.LDAPAttributes.PreferredUsernameAttribute))
	}
	if wm.LDAPAttributes.EmailAttribute != "" {
		opts = append(opts, ldap.WithEmailAttribute(wm.LDAPAttributes.EmailAttribute))
	}
	if wm.LDAPAttributes.EmailVerifiedAttribute != "" {
		opts = append(opts, ldap.WithEmailVerifiedAttribute(wm.LDAPAttributes.EmailVerifiedAttribute))
	}
	if wm.LDAPAttributes.PhoneAttribute != "" {
		opts = append(opts, ldap.WithPhoneAttribute(wm.LDAPAttributes.PhoneAttribute))
	}
	if wm.LDAPAttributes.PhoneVerifiedAttribute != "" {
		opts = append(opts, ldap.WithPhoneVerifiedAttribute(wm.LDAPAttributes.PhoneVerifiedAttribute))
	}
	if wm.LDAPAttributes.PreferredLanguageAttribute != "" {
		opts = append(opts, ldap.WithPreferredLanguageAttribute(wm.LDAPAttributes.PreferredLanguageAttribute))
	}
	if wm.LDAPAttributes.AvatarURLAttribute != "" {
		opts = append(opts, ldap.WithAvatarURLAttribute(wm.LDAPAttributes.AvatarURLAttribute))
	}
	if wm.LDAPAttributes.ProfileAttribute != "" {
		opts = append(opts, ldap.WithProfileAttribute(wm.LDAPAttributes.ProfileAttribute))
	}
	if wm.IsCreationAllowed {
		opts = append(opts, ldap.WithCreationAllowed())
	}
	if wm.IsLinkingAllowed {
		opts = append(opts, ldap.WithLinkingAllowed())
	}
	if wm.IsAutoCreation {
		opts = append(opts, ldap.WithAutoCreation())
	}
	if wm.IsAutoUpdate {
		opts = append(opts, ldap.WithAutoUpdate())
	}
	return ldap.New(
		wm.Name,
		wm.Servers,
		wm.BaseDN,
		wm.BindDN,
		password,
		wm.UserBase,
		wm.UserObjectClasses,
		wm.UserFilters,
		wm.Timeout,
		callbackURL,
		opts...,
	), nil
}

type IDPRemoveWriteModel struct {
	eventstore.WriteModel

	ID    string
	State domain.IDPState
}

func (wm *IDPRemoveWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *idp.OAuthIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.OIDCIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.JWTIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.AzureADIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.GitHubIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.GitHubEnterpriseIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.GitLabIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.GitLabSelfHostedIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.GoogleIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.LDAPIDPAddedEvent:
			wm.reduceAdded(e.ID)
		case *idp.RemovedEvent:
			wm.reduceRemoved(e.ID)
		case *idpconfig.IDPConfigAddedEvent:
			wm.reduceAdded(e.ConfigID)
		case *idpconfig.IDPConfigRemovedEvent:
			wm.reduceRemoved(e.ConfigID)
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *IDPRemoveWriteModel) reduceAdded(id string) {
	if wm.ID != id {
		return
	}
	wm.State = domain.IDPStateActive
}

func (wm *IDPRemoveWriteModel) reduceRemoved(id string) {
	if wm.ID != id {
		return
	}
	wm.State = domain.IDPStateRemoved
}

type IDPTypeWriteModel struct {
	eventstore.WriteModel

	ID    string
	Type  domain.IDPType
	State domain.IDPState
}

func NewIDPTypeWriteModel(id string) *IDPTypeWriteModel {
	return &IDPTypeWriteModel{
		ID: id,
	}
}

func (wm *IDPTypeWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *instance.OAuthIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeOAuth, e.Aggregate())
		case *org.OAuthIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeOAuth, e.Aggregate())
		case *instance.OIDCIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeOIDC, e.Aggregate())
		case *org.OIDCIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeOIDC, e.Aggregate())
		case *instance.JWTIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeJWT, e.Aggregate())
		case *org.JWTIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeJWT, e.Aggregate())
		case *instance.AzureADIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeAzureAD, e.Aggregate())
		case *org.AzureADIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeAzureAD, e.Aggregate())
		case *instance.GitHubIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitHub, e.Aggregate())
		case *org.GitHubIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitHub, e.Aggregate())
		case *instance.GitHubEnterpriseIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitHubEnterprise, e.Aggregate())
		case *org.GitHubEnterpriseIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitHubEnterprise, e.Aggregate())
		case *instance.GitLabIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitLab, e.Aggregate())
		case *org.GitLabIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitLab, e.Aggregate())
		case *instance.GitLabSelfHostedIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitLabSelfHosted, e.Aggregate())
		case *org.GitLabSelfHostedIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGitLabSelfHosted, e.Aggregate())
		case *instance.GoogleIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGoogle, e.Aggregate())
		case *org.GoogleIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeGoogle, e.Aggregate())
		case *instance.LDAPIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeLDAP, e.Aggregate())
		case *org.LDAPIDPAddedEvent:
			wm.reduceAdded(e.ID, domain.IDPTypeLDAP, e.Aggregate())
		case *instance.IDPRemovedEvent:
			wm.reduceRemoved(e.ID)
		case *org.IDPRemovedEvent:
			wm.reduceRemoved(e.ID)
		case *instance.IDPConfigAddedEvent:
			if e.Typ == domain.IDPConfigTypeOIDC {
				wm.reduceAdded(e.ConfigID, domain.IDPTypeOIDC, e.Aggregate())
			} else if e.Typ == domain.IDPConfigTypeJWT {
				wm.reduceAdded(e.ConfigID, domain.IDPTypeJWT, e.Aggregate())
			}
		case *org.IDPConfigAddedEvent:
			if e.Typ == domain.IDPConfigTypeOIDC {
				wm.reduceAdded(e.ConfigID, domain.IDPTypeOIDC, e.Aggregate())
			} else if e.Typ == domain.IDPConfigTypeJWT {
				wm.reduceAdded(e.ConfigID, domain.IDPTypeJWT, e.Aggregate())
			}
		case *instance.IDPConfigRemovedEvent:
			wm.reduceRemoved(e.ConfigID)
		case *org.IDPConfigRemovedEvent:
			wm.reduceRemoved(e.ConfigID)
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *IDPTypeWriteModel) reduceAdded(id string, t domain.IDPType, agg eventstore.Aggregate) {
	if wm.ID != id {
		return
	}
	wm.Type = t
	wm.State = domain.IDPStateActive
	wm.ResourceOwner = agg.ResourceOwner
	wm.InstanceID = agg.InstanceID
}

func (wm *IDPTypeWriteModel) reduceRemoved(id string) {
	if wm.ID != id {
		return
	}
	wm.Type = domain.IDPTypeUnspecified
	wm.State = domain.IDPStateRemoved
	wm.ResourceOwner = ""
	wm.InstanceID = ""
}

func (wm *IDPTypeWriteModel) Query() *eventstore.SearchQueryBuilder {
	return eventstore.NewSearchQueryBuilder(eventstore.ColumnsEvent).
		AddQuery().
		AggregateTypes(instance.AggregateType).
		EventTypes(
			instance.OAuthIDPAddedEventType,
			instance.OIDCIDPAddedEventType,
			instance.JWTIDPAddedEventType,
			instance.AzureADIDPAddedEventType,
			instance.GitHubIDPAddedEventType,
			instance.GitHubEnterpriseIDPAddedEventType,
			instance.GitLabIDPAddedEventType,
			instance.GitLabSelfHostedIDPAddedEventType,
			instance.GoogleIDPAddedEventType,
			instance.LDAPIDPAddedEventType,
			instance.IDPRemovedEventType,
		).
		EventData(map[string]interface{}{"id": wm.ID}).
		Or().
		AggregateTypes(org.AggregateType).
		EventTypes(
			org.OAuthIDPAddedEventType,
			org.OIDCIDPAddedEventType,
			org.JWTIDPAddedEventType,
			org.AzureADIDPAddedEventType,
			org.GitHubIDPAddedEventType,
			org.GitHubEnterpriseIDPAddedEventType,
			org.GitLabIDPAddedEventType,
			org.GitLabSelfHostedIDPAddedEventType,
			org.GoogleIDPAddedEventType,
			org.LDAPIDPAddedEventType,
			org.IDPRemovedEventType,
		).
		EventData(map[string]interface{}{"id": wm.ID}).
		Or(). // old events
		AggregateTypes(instance.AggregateType).
		EventTypes(
			instance.IDPConfigAddedEventType,
			instance.IDPConfigRemovedEventType,
		).
		EventData(map[string]interface{}{"idpConfigId": wm.ID}).
		Or().
		AggregateTypes(org.AggregateType).
		EventTypes(
			org.IDPConfigAddedEventType,
			org.IDPConfigRemovedEventType,
		).
		EventData(map[string]interface{}{"idpConfigId": wm.ID}).
		Builder()
}

type IDP interface {
	eventstore.QueryReducer
	ToProvider(string, crypto.EncryptionAlgorithm) (providers.Provider, error)
}

type AllIDPWriteModel struct {
	model IDP

	ID            string
	IDPType       domain.IDPType
	ResourceOwner string
	Instance      bool
}

func NewAllIDPWriteModel(resourceOwner string, instanceBool bool, id string, idpType domain.IDPType) (*AllIDPWriteModel, error) {
	writeModel := &AllIDPWriteModel{
		ID:            id,
		IDPType:       idpType,
		ResourceOwner: resourceOwner,
		Instance:      instanceBool,
	}

	if instanceBool {
		switch idpType {
		case domain.IDPTypeOIDC:
			writeModel.model = NewOIDCInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeJWT:
			writeModel.model = NewJWTInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeOAuth:
			writeModel.model = NewOAuthInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeLDAP:
			writeModel.model = NewLDAPInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeAzureAD:
			writeModel.model = NewAzureADInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitHub:
			writeModel.model = NewGitHubInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitHubEnterprise:
			writeModel.model = NewGitHubEnterpriseInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitLab:
			writeModel.model = NewGitLabInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitLabSelfHosted:
			writeModel.model = NewGitLabSelfHostedInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGoogle:
			writeModel.model = NewGoogleInstanceIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeUnspecified:
			fallthrough
		default:
			return nil, errors.ThrowInternal(nil, "COMMAND-xw921211", "Errors.IDPConfig.NotExisting")
		}
	} else {
		switch idpType {
		case domain.IDPTypeOIDC:
			writeModel.model = NewOIDCOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeJWT:
			writeModel.model = NewJWTOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeOAuth:
			writeModel.model = NewOAuthOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeLDAP:
			writeModel.model = NewLDAPOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeAzureAD:
			writeModel.model = NewAzureADOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitHub:
			writeModel.model = NewGitHubOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitHubEnterprise:
			writeModel.model = NewGitHubEnterpriseOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitLab:
			writeModel.model = NewGitLabOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGitLabSelfHosted:
			writeModel.model = NewGitLabSelfHostedOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeGoogle:
			writeModel.model = NewGoogleOrgIDPWriteModel(resourceOwner, id)
		case domain.IDPTypeUnspecified:
			fallthrough
		default:
			return nil, errors.ThrowInternal(nil, "COMMAND-xw921111", "Errors.IDPConfig.NotExisting")
		}
	}
	return writeModel, nil
}

func (wm *AllIDPWriteModel) Reduce() error {
	return wm.model.Reduce()
}

func (wm *AllIDPWriteModel) Query() *eventstore.SearchQueryBuilder {
	return wm.model.Query()
}

func (wm *AllIDPWriteModel) AppendEvents(events ...eventstore.Event) {
	wm.model.AppendEvents(events...)
}

func (wm *AllIDPWriteModel) ToProvider(callbackURL string, idpAlg crypto.EncryptionAlgorithm) (providers.Provider, error) {
	return wm.model.ToProvider(callbackURL, idpAlg)
}
